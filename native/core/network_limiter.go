package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type requestGate struct {
	busy      bool
	lastStart time.Time
}

type requestBackoff struct {
	host   string
	until  time.Time
	status int
	reason string
}

func (backoff *requestBackoff) Error() string {
	reason := ""
	if backoff.reason != "" {
		reason = "：" + backoff.reason
	}
	seconds := max(1, int(time.Until(backoff.until).Seconds()+0.999))
	return fmt.Sprintf("%s HTTP %d%s，请在 %d 秒后重试", backoff.host, backoff.status, reason, seconds)
}

func catalogResponseBlockReason(response *http.Response, body []byte) string {
	if len(body) > 64*1024 {
		body = body[:64*1024]
	}
	page := strings.ToLower(string(body))
	reason := ""
	if strings.Contains(page, "cloudflare") && (strings.Contains(page, "sorry, you have been blocked") || strings.Contains(page, "you are unable to access")) {
		reason = "Cloudflare 拒绝了当前请求"
	} else if strings.EqualFold(response.Header.Get("Cf-Mitigated"), "challenge") || strings.Contains(page, "_cf_chl_opt") || strings.Contains(page, "<title>just a moment") && strings.Contains(page, "cloudflare") {
		reason = "站点要求浏览器验证，当前请求无法通过"
	} else if strings.Contains(page, `name="style-origin"`) || strings.Contains(page, "name='style-origin'") {
		// 野果的镜像站会随机返回这种"JS 挑战页"：只有一个 <meta name="style-origin">
		// 加一段 Base64 解码脚本，没有任何真实内容。0.8.9 之前它被当成正常页面返回，
		// 于是镜像竞速有一半概率"赢"在挑战页上，密钥发现直接失败（实测 3/5）。
		reason = "站点返回了反爬验证页，当前请求无法通过"
	} else if strings.Contains(page, "document.write(base64.decode(") && strings.Contains(page, "<noscript>error") {
		// 另一类兜底页：整页只有 document.write(Base64.decode("..."))，解出来是
		// "51短剧官方入口"（51hub.com / 51shipin.cc），与野果毫无关系。
		// 镜像竞速只看"HTTP 200 且有内容"，这种页会抢先赢下竞速，导致
		// 首页 payload 解码失败、密钥发现报"配置已变化"（实测 1/5~5/6）。
		reason = catalogBlockReasonInterstitial
	}
	return reason
}

// catalogBlockReasonInterstitial 是"站点随机塞兜底页"的情况：同一个域名下一
// 秒就可能恢复正常，不是真的被封。所以它不能触发 host 级退避——一旦给域名
// 挂上 60 秒退避，密钥发现会连续失败，整个站源直接不可用。
const catalogBlockReasonInterstitial = "站点返回了入口导航页，当前请求被拦截"

func (d *Downloader) catalogResponseError(request *http.Request, response *http.Response, body []byte) error {
	reason := catalogResponseBlockReason(response, body)
	if d.limiter != nil && reason != "" && reason != catalogBlockReasonInterstitial && response.StatusCode >= 200 && response.StatusCode < 300 {
		d.limiter.observeBlock(request, response)
	}

	host := strings.ToLower(request.URL.Hostname())
	if d.limiter != nil && (response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests || reason != "") {
		d.limiter.mu.Lock()
		backoffs := d.limiter.backoffs
		if background, _ := request.Context().Value(backgroundCatalogKey{}).(bool); background && response.StatusCode != http.StatusTooManyRequests {
			backoffs = d.limiter.backgroundBackoffs
		}
		if previous := backoffs[host]; previous != nil && previous.status == response.StatusCode {
			updated := *previous
			if reason != "" {
				updated.reason = reason
			}
			backoffs[host] = &updated
			d.limiter.mu.Unlock()
			return &updated
		}
		d.limiter.mu.Unlock()
	}
	if reason != "" {
		return fmt.Errorf("%s HTTP %d：%s", host, response.StatusCode, reason)
	}
	return fmt.Errorf("%s HTTP %d", host, response.StatusCode)
}

type requestLimiter struct {
	concurrency        int
	active             int
	foregroundWaiting  int
	lastForeground     time.Time
	interval           time.Duration
	mu                 sync.Mutex
	gates              map[string]*requestGate
	backoffs           map[string]*requestBackoff
	backgroundBackoffs map[string]*requestBackoff
	changed            chan struct{}
}

type backgroundCatalogKey struct{}

// playbackPriorityKey 标记"前台播放取流"请求，让它在 limiter.acquire 里拥有
// 越过排队的最高优先级。子 ctx 会继承这个 Value，所以只要在打了标记的 ctx
// 下派生，设备注册、取流路由、CDN 头部探测就都自动带上它。
//
// 为什么播放取流必须优先（0.8.28 连播卡死修复）：
//
// 用户现象是「红果连播很多集后，突然某一集无法播放；切到后一集能播；重启
// App 后原来那集又能播」。根因链：每集切集会异步起弹幕预取与预载
// （app_preload.go，session 是 preload-<集号>，engine.reads 完成后不删、
// 按集号线性累积），这些后台请求与前台取流共用同一个 requestLimiter ——
// gate.busy 按 source 单布尔、并发额度 3、interval 250ms，且 release 绑在
// response.Body.Close 上。队列一长，播放那条 6 秒整轮预算（注册新身份 +
// 3 条路由单发 + 兜底重注册，provider_hongguo_native_media.go）就在 acquire
// 排队阶段耗尽，被吞成空正文；前端只对 media_transient 静默重试一次，窗口
// 没过就硬报错。切到下一集会 cancel 掉旧预载、清空队列，重启会清零
// limiter/reads/goroutine —— 正好解释了全部现象。
//
// 因此带此标记的请求在 acquire 里跳过 gate.busy、active<concurrency、
// interval/delay 三项排队限制，只保留两道必要防线：host 级退避（backoffs /
// backgroundBackoffs）和 ctx.Err()。同时它从未占用 gate.busy / gate.lastStart，
// release 也绝不能去清这两项 —— 否则会误放行本应继续排队的同 source 请求，
// 破坏单 host 串行化的初衷。
type playbackPriorityKey struct{}

// withPlaybackPriority 给 ctx 打上前台播放取流优先级标记。
func withPlaybackPriority(ctx context.Context) context.Context {
	return context.WithValue(ctx, playbackPriorityKey{}, true)
}

// skipCatalogObserveKey 标记"不要把响应记成 host 级封禁"的请求。
//
// 红果 App 接口（api5-normal-sinfonlineb.fqnovel.com）的 4xx 绝大多数来自
// 签名随机失配：客户端每次请求现算一套 x-gorgon / x-argus，算错了服务端就
// 回 403，换一套签名重试通常就过。把它当成"站点封禁"记成 1~30 分钟的 host
// 退避，后果是一次偶发 403 之后，同一域名下的详情、取流、弹幕**全部**在
// acquire 阶段直接失败 —— 用户看到的就是"刚才还能看，突然就全都请求失败，
// 过一分钟又自己好了"。
//
// 页面目录（HTML 站源）继续走 observe：那边 403 才是真的被拦。
type skipCatalogObserveKey struct{}

// skipCatalogGateKey 标记"镜像竞速请求"，不参与全局并发与间隔节流。
//
// 原因：黄果AI 的镜像站时通时不通，只有真正并行撞上去才有可能命中可用节点；
// 若被并发限制器压成 3 个一批，8 个候选会退化成三批串行，成功率反而更低。
type skipCatalogGateKey struct{}

func newRequestLimiter(concurrency int, interval time.Duration) *requestLimiter {
	if concurrency < 1 {
		concurrency = 1
	}
	return &requestLimiter{concurrency: concurrency, interval: interval, gates: map[string]*requestGate{}, backoffs: map[string]*requestBackoff{}, backgroundBackoffs: map[string]*requestBackoff{}, changed: make(chan struct{})}
}

func (limiter *requestLimiter) notifyLocked() {
	close(limiter.changed)
	limiter.changed = make(chan struct{})
}

func (limiter *requestLimiter) acquire(ctx context.Context, request *http.Request) (func(), error) {
	if ctx.Value(skipCatalogGateKey{}) != nil {
		// 镜像竞速请求：只受请求自身的 context 超时约束。
		return func() {}, nil
	}
	background, _ := ctx.Value(backgroundCatalogKey{}).(bool)
	playbackPriority, _ := ctx.Value(playbackPriorityKey{}).(bool)
	host := strings.ToLower(request.URL.Hostname())
	source := providerSourceForURL(request.URL.String())
	if source == "" {
		source = host
		if strings.HasSuffix(host, ".cloudfront.net") {
			source = "cloudfront"
		}
	}
	limiter.mu.Lock()
	if !background {
		limiter.foregroundWaiting++
		defer func() {
			limiter.mu.Lock()
			limiter.foregroundWaiting--
			limiter.notifyLocked()
			limiter.mu.Unlock()
		}()
	}
	gate := limiter.gates[source]
	if gate == nil {
		gate = &requestGate{}
		limiter.gates[source] = gate
	}
	limiter.mu.Unlock()
	if playbackPriority {
		// 前台播放取流：不排队，直接放行（详见 playbackPriorityKey 的说明）。
		// 仍要认 host 级退避与调用方 ctx，不能把这两道防线一起绕掉。
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		limiter.mu.Lock()
		if backoff := limiter.backoffs[host]; backoff != nil && time.Now().Before(backoff.until) {
			limiter.mu.Unlock()
			return nil, backoff
		}
		if backoff := limiter.backgroundBackoffs[host]; background && backoff != nil && time.Now().Before(backoff.until) {
			limiter.mu.Unlock()
			return nil, backoff
		}
		limiter.active++
		limiter.mu.Unlock()
		var once sync.Once
		return func() {
			once.Do(func() {
				limiter.mu.Lock()
				limiter.active--
				// 优先级请求没占过 gate.busy / gate.lastStart，这里刻意不动它们，
				// 否则会误放行本应继续排队的同 source 请求。
				if !background {
					limiter.lastForeground = time.Now()
				}
				limiter.notifyLocked()
				limiter.mu.Unlock()
			})
		}, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		limiter.mu.Lock()
		if backoff := limiter.backoffs[host]; backoff != nil && time.Now().Before(backoff.until) {
			limiter.mu.Unlock()
			return nil, backoff
		}
		if backoff := limiter.backgroundBackoffs[host]; background && backoff != nil && time.Now().Before(backoff.until) {
			limiter.mu.Unlock()
			return nil, backoff
		}
		delay := time.Until(gate.lastStart.Add(limiter.interval))
		if background {

			if idleDelay := time.Until(limiter.lastForeground.Add(limiter.interval)); idleDelay > delay {
				delay = idleDelay
			}
		}
		lowPriorityReady := !background || limiter.foregroundWaiting == 0 && limiter.active == 0
		if lowPriorityReady && !gate.busy && limiter.active < limiter.concurrency && delay <= 0 {
			gate.busy = true
			gate.lastStart = time.Now()
			limiter.active++
			limiter.mu.Unlock()
			break
		}
		changed := limiter.changed
		limiter.mu.Unlock()
		var timer *time.Timer
		var deadline <-chan time.Time
		if delay > 0 {
			timer = time.NewTimer(delay)
			deadline = timer.C
		}
		select {
		case <-changed:
		case <-deadline:
		case <-ctx.Done():
		}
		if timer != nil {
			timer.Stop()
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			limiter.mu.Lock()
			limiter.active--
			gate.busy = false
			if !background {
				limiter.lastForeground = time.Now()
			}
			limiter.notifyLocked()
			limiter.mu.Unlock()
		})
	}, nil
}

// clearBackoffs 清掉所有 host 级退避（0.8.10）。
//
// 使用场景：用户明确要求"重新拉一次"（下拉刷新、重试、检查链接），
// 或者一次目录请求**因为退避**直接失败。
//
// 为什么可以整体清掉：退避只是一层保护，它的前提是"站点在拒绝我们"；
// 而镜像竞速本来就会换 host 重试，长期挂着一个 30 分钟的退避只会让
// 站源彻底不可用（用户看到的是永远转圈，只有手动检查链接才能救回来）。
func (limiter *requestLimiter) clearBackoffs() {
	limiter.mu.Lock()
	limiter.backoffs = map[string]*requestBackoff{}
	limiter.backgroundBackoffs = map[string]*requestBackoff{}
	limiter.notifyLocked()
	limiter.mu.Unlock()
}

// clearBackoff 只清掉某个 host 的退避。
//
// 用于"这条链路自己知道刚才的失败是签名/瞬时问题"的场景（红果 App 接口），
// 避免为了一次 403 把无关站源的退避一起清掉。
func (limiter *requestLimiter) clearBackoff(host string) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return
	}
	limiter.mu.Lock()
	delete(limiter.backoffs, host)
	delete(limiter.backgroundBackoffs, host)
	limiter.notifyLocked()
	limiter.mu.Unlock()
}

func (limiter *requestLimiter) observe(request *http.Request, response *http.Response) {
	if response.StatusCode != http.StatusForbidden && response.StatusCode != http.StatusTooManyRequests && !strings.EqualFold(response.Header.Get("Cf-Mitigated"), "challenge") {
		return
	}
	limiter.observeBlock(request, response)
}

func (limiter *requestLimiter) observeBlock(request *http.Request, response *http.Response) {
	delay := time.Minute
	if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
		delay = time.Duration(seconds) * time.Second
	} else if until, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil && time.Until(until) > 0 {
		delay = time.Until(until)
	}
	if delay > 30*time.Minute {
		delay = 30 * time.Minute
	}
	host := strings.ToLower(request.URL.Hostname())
	limiter.mu.Lock()
	backoffs := limiter.backoffs
	if background, _ := request.Context().Value(backgroundCatalogKey{}).(bool); background && response.StatusCode != http.StatusTooManyRequests {

		backoffs = limiter.backgroundBackoffs
	}
	backoffs[host] = &requestBackoff{host: host, until: time.Now().Add(delay), status: response.StatusCode}
	limiter.notifyLocked()
	limiter.mu.Unlock()
}

type limitedResponseBody struct {
	io.ReadCloser
	release func()
}

func (body *limitedResponseBody) Close() error {
	err := body.ReadCloser.Close()
	body.release()
	return err
}

func (d *Downloader) doCatalogRequestWithTimeout(request *http.Request, timeout time.Duration) (*http.Response, error) {
	return d.doPreparedCatalogRequest(request, timeout, nil)
}

func (d *Downloader) doPreparedCatalogRequest(request *http.Request, timeout time.Duration, prepare func(*http.Request)) (*http.Response, error) {
	if background, _ := request.Context().Value(backgroundCatalogKey{}).(bool); background && (timeout <= 0 || timeout > 8*time.Second) {
		timeout = 8 * time.Second
		// 黄果剧场（huangguoai.fit）经 Cloudflare，直连响应常需 10~25 秒；
		// 按后台 8 秒上限会稳定误判超时，这里单独放宽。
		if strings.HasSuffix(strings.ToLower(request.URL.Hostname()), "huangguoai.fit") {
			timeout = 25 * time.Second
		}
	}
	base := request.Context()
	release, err := d.limiter.acquire(base, request)
	if err != nil {
		return nil, err
	}
	cancel := func() {}
	requestCtx := base
	if timeout > 0 {
		requestCtx, cancel = context.WithTimeout(base, timeout)
	}
	request = request.WithContext(requestCtx)
	if prepare != nil {
		prepare(request)
	}
	response, err := d.client.Do(request)
	if err != nil && base.Err() == nil {
		// 用户开了系统代理/VPN 时，代理节点可能连不上国内站源（或出口 IP 被站点限流）。
		// 此时用同名请求强制直连再试一次，避免"一开代理，站源更新全失败"。
		// proxyRouter 在部分构造路径（纯直连场景、单测）里为 nil，这里必须判空，
		// 否则 nil 接收者调用 proxy() 会直接 panic。
		if d.proxyRouter != nil {
			if proxy, proxyErr := d.proxyRouter.proxy(request); proxyErr == nil && proxy != nil {
				cancel()
				directCtx := context.WithValue(base, forceDirectKey{}, true)
				if timeout > 0 {
					var directCancel context.CancelFunc
					directCtx, directCancel = context.WithTimeout(directCtx, timeout)
					cancel = directCancel
				}
				response, err = d.client.Do(request.Clone(directCtx))
			}
		}
	}
	if err != nil {
		cancel()
		release()
		return nil, err
	}
	// 红果 App 接口的 4xx 是签名失配，不是封禁，不能计入 host 退避
	// （见 skipCatalogObserveKey 的说明）。
	if request.Context().Value(skipCatalogObserveKey{}) == nil {
		d.limiter.observe(request, response)
	}
	observeSourceResponse(request.Context(), response)
	response.Body = &limitedResponseBody{ReadCloser: response.Body, release: func() { cancel(); release() }}
	return response, nil
}
