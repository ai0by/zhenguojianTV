package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const hongguoAppBaseURL = "https://api5-normal-sinfonlineb.fqnovel.com"

// hongguoAppFallbackHost 是备用入口域名（0.8.24 新增）。
//
// 上游会按域名做灰度和分区限流：主域某一刻对某台设备返回空正文时，备用域
// 往往还是通的。实测同一次取流请求，主域空、备域拿到完整 video_model。
const hongguoAppFallbackHost = "api5-normal-hl.fqnovel.com"

// hongguoAppUserAgent 必须与下面的 versionCode 一致（UA 里也带着版本号）。
//
// **0.8.24 关键修正**：这是「红果取不到片源」的真正根因。
//
// 之前用的是 73532/7.3.5.32 配 Cronet/TTNet 尾巴的 UA，取流接口对它的回复
// 是「200 + 空正文」——实测 30 次 0 中。换成 71532/7.1.3.32 配 `tt-ok` 尾巴
// 之后，同样的签名、同样的设备，立刻拿到完整 video_model（含 5 档清晰度）。
//
// 用户侧表现就是"红果越来越卡"：旧 UA 拿不到片源，代码于是重试 5 次
// （跨窗退避 0.8/1.4/2.0/2.5s）→ 再取一次 → 再换设备注册 → 再取一次，
// 最坏纯等 20 秒才报错。换 UA 之后第一次就中，0.6 秒出片。
const hongguoAppUserAgent = "com.phoenix.read/71532 (Linux; U; Android 9; SM-N9860; Build/PQ3A.190705.10241111;tt-ok/3.12.13.20)"

// 客户端版本号：query 里的 version_code / version_name、UA、以及 medusa
// 签名指纹三者必须一致，所以只在这里写一次。
//
// 7.1.3.32 是实测能通过取流接口准入的版本（见上面的 UA 注释）。
// **不要单独调高它**：version_code 参与 sign6 的 medusa 签名和 _rticket 的
// 分支计算，实测更高版本会被打回空正文。
const (
	hongguoAppVersionCode = "71332"
	hongguoAppVersionName = "7.1.3.32"
)

type hongguoCatalogCursor struct {
	Offset        int       `json:"offset"`
	SessionID     string    `json:"sessionId,omitempty"`
	LastID        string    `json:"lastId,omitempty"`
	PageSignature string    `json:"pageSignature,omitempty"`
	Initialized   bool      `json:"initialized"`
	Exhausted     bool      `json:"exhausted"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// hongguoCatalogStateVersion 是红果 App 侧状态的版本号。
//
// 1 → 2（0.8.14）：多了 Registered 字段。Version 1 里存的 DeviceID 是本地
// 随机生成的，服务端不认（取流返回空正文），所以旧状态必须丢弃重注册。
const hongguoCatalogStateVersion = 2

type hongguoCatalogState struct {
	Version   int    `json:"version"`
	DeviceID  string `json:"deviceId"`
	InstallID string `json:"installId"`
	// Registered 表示 DeviceID/InstallID 是向服务端注册过的真实设备号。
	// 0.8.14 之前这两个值是本地随机生成的，服务端不认 —— 取流接口会返回
	// 200 + 空正文。旧的持久化数据（Version 1）一律视为未注册，重新注册。
	Registered bool                            `json:"registered,omitempty"`
	Feeds      map[string]hongguoCatalogCursor `json:"feeds"`
}

type hongguoAppClient struct {
	mu        sync.Mutex
	catalogMu sync.Mutex
	// deviceMu 串行化游客设备注册，避免并发取流时重复注册（0.8.14）。
	deviceMu sync.Mutex
	// deviceRetryAfter / deviceErr 是注册失败后的冷却窗口（0.8.14）。
	// 网络不通时注册会连续超时，不能让每次播放都重试满 3 次 —— 冷却期内
	// 直接返回上次的错误，用户立刻看到原因而不是转圈。
	deviceRetryAfter time.Time
	deviceErr        error
	baseURL          string
	state            hongguoCatalogState
	details          map[string]hongguoDetailEntry
	pending          map[string]*hongguoDetailCall
	searches         map[string]hongguoSearchEntry
	searchPending    map[string]*hongguoSearchCall
	suggestions      map[string]hongguoSuggestionEntry
	suggestPending   map[string]*hongguoSuggestionCall
	// 弹幕（0.8.11）：按 "剧集:视频:起:止" 缓存 5 分钟，失败缓存 15 秒，
	// 避免同一段反复拖动时把请求打爆。
	danmaku        map[string]hongguoDanmakuCacheEntry
	danmakuPending map[string]*hongguoDanmakuCall
}

func (downloader *Downloader) hongguoClient() *hongguoAppClient {
	downloader.hongguoOnce.Do(func() {
		downloader.hongguo = &hongguoAppClient{
			baseURL: hongguoAppBaseURL,
			state:   hongguoCatalogState{Version: hongguoCatalogStateVersion, DeviceID: newHongguoDeviceID(), InstallID: newHongguoDeviceID(), Feeds: map[string]hongguoCatalogCursor{}},
			details: map[string]hongguoDetailEntry{}, pending: map[string]*hongguoDetailCall{},
			searches: map[string]hongguoSearchEntry{}, searchPending: map[string]*hongguoSearchCall{},
			suggestions: map[string]hongguoSuggestionEntry{}, suggestPending: map[string]*hongguoSuggestionCall{},
			danmaku: map[string]hongguoDanmakuCacheEntry{}, danmakuPending: map[string]*hongguoDanmakuCall{},
		}
	})
	return downloader.hongguo
}

func cloneHongguoCatalogState(state *hongguoCatalogState) *hongguoCatalogState {
	if state == nil {
		return nil
	}
	cloned := *state
	cloned.Feeds = make(map[string]hongguoCatalogCursor, len(state.Feeds))
	for name, cursor := range state.Feeds {
		cloned.Feeds[name] = cursor
	}
	return &cloned
}

func (downloader *Downloader) restoreHongguoCatalog(state *hongguoCatalogState) {
	if state == nil || state.Version != hongguoCatalogStateVersion || !hongguoNumericID.MatchString(state.DeviceID) || !hongguoNumericID.MatchString(state.InstallID) {
		return
	}
	cloned := cloneHongguoCatalogState(state)
	for name, cursor := range cloned.Feeds {
		known := false
		for _, genre := range hongguoAppGenres {
			known = known || name == genre.key || name == "category:"+genre.key
		}
		if !known || !cursor.Initialized || cursor.Offset < 0 || cursor.Offset > 1_000_000 || len(cursor.SessionID) > 4096 || len(cursor.LastID) > 256 || len(cursor.PageSignature) > 64 {
			delete(cloned.Feeds, name)
		}
	}
	client := downloader.hongguoClient()
	client.catalogMu.Lock()
	defer client.catalogMu.Unlock()
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.state.Feeds) == 0 {
		client.state = *cloned
	}
}

func (downloader *Downloader) hongguoCatalogSnapshot() *hongguoCatalogState {
	client := downloader.hongguoClient()
	client.mu.Lock()
	defer client.mu.Unlock()
	return cloneHongguoCatalogState(&client.state)
}

func hongguoCatalogInitialized(state *hongguoCatalogState) bool {
	if state != nil && state.Version == hongguoCatalogStateVersion {
		for _, cursor := range state.Feeds {
			if cursor.Initialized {
				return true
			}
		}
	}
	return false
}

func hongguoCatalogHasMore(state *hongguoCatalogState) bool {
	for _, genre := range hongguoAppGenres {
		if state == nil || !state.Feeds[genre.key].Exhausted {
			return true
		}
	}
	return false
}

func (downloader *Downloader) hongguoAppRequest(ctx context.Context, method, path string, extra url.Values, payload any) (map[string]any, error) {
	// 这条链路的所有重试都由自己管，因此不参与 host 级封禁统计
	// （红果 App 接口的 4xx 基本是签名随机失配，不是站点封禁）。
	ctx = context.WithValue(ctx, skipCatalogObserveKey{}, true)
	comment, _ := ctx.Value(hongguoCommentKey{}).(bool)
	// 单次调用的总预算。
	//
	// 以前这里只有"每次尝试 20 秒 × 最多 8 次"，正常情况下弹幕/详情几百毫秒
	// 就回，真出问题时会一路耗到外层 60 秒超时 —— 用户看到的是转圈，然后
	// 一个没有指向性的"读取失败"。把预算收到十几秒，失败得更快，前端也就
	// 有机会用新的随机签名重来一次。
	budget := 25 * time.Second
	if comment {
		// 弹幕是"当下就要"的数据：30 秒窗口的弹幕晚到 15 秒就没有意义了。
		budget = 15 * time.Second
	}
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < budget {
			budget = remaining
		}
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	client := downloader.hongguoClient()
	client.mu.Lock()
	base, deviceID, installID := client.baseURL, client.state.DeviceID, client.state.InstallID
	registered := client.state.Registered
	client.mu.Unlock()
	query := url.Values{
		"aid": {"8662"}, "app_name": {"novelread"}, "version_code": {hongguoAppVersionCode}, "version_name": {hongguoAppVersionName},
		"manifest_version_code": {hongguoAppVersionCode}, "update_version_code": {hongguoAppVersionCode}, "channel": {"update_64"},
		"device_platform": {"android"}, "os": {"android"}, "ssmix": {"a"}, "device_type": {"25053RT47C"},
		"device_brand": {"Redmi"}, "language": {"zh"}, "os_api": {"36"}, "os_version": {"16"},
		"resolution": {"1280*2772"}, "dpi": {"520"}, "ac": {"wifi"}, "device_id": {deviceID}, "iid": {installID},
	}
	for key, values := range extra {
		query[key] = append([]string(nil), values...)
	}
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}
	// 空响应耗时很短，属于可重试的临时状态，因此上限放宽到 8 次。
	attempts := downloader.cfg.Retries
	if attempts < 1 {
		attempts = 1
	}
	if attempts < 6 {
		attempts = 6
	}
	if attempts > 8 {
		attempts = 8
	}
	var lastErr error
	// 空正文的重试预算（0.8.16 第四批 / 0.8.22 / 0.8.23 调整）。
	//
	// 空正文有两种成因：签名随机失配（多试几次能中），以及**服务端不认这个
	// device_id**（试多少次都是空）。设备已经注册过时，第二种占多数 ——
	// 这时继续重试只是在拖时间。
	//
	// 空正文的重试节奏（0.8.24 重排）。
	//
	// 历史教训：0.8.23 认为空正文是"服务端按 0.5~1.5 秒时间片周期性丢弃"，
	// 于是把重试改成 0.8/1.4/2.0/2.5 秒跨窗退避。那个结论建立在**UA 已经
	// 失效**的前提下 —— 当时成功率只有 68%，看起来像随机失败窗，其实是
	// 准入被拒。换了正确的 UA/版本后单发成功率接近 100%。
	//
	// 但**不能改成快速连发**：实测同一 device_id 连续快速重试反而把
	// 成功率打到 2/5，服务端会按身份限流。正确做法是参考项验证过的
	// 「换维度」：先换域名，再换身份，每次都留 250ms 以上间隔。
	emptyBudget := attempts
	if registered {
		emptyBudget = 5
	} else {
		// 未注册设备的空正文 = "服务端根本不认这台设备"，重试救不回来
		// （注册这条路由 ensureHongguoDevice 管，失败有 2 分钟冷却）。
		// 旧代码也重试满 6~8 次，配上跨窗退避就是白等十秒；收到 3 次
		// 就停，错误尽早交给上层去走换设备 / 短路路径。
		emptyBudget = 3
	}
	emptyCount := 0
	// emptyRetry 标记上一次失败是否为空正文：空正文用跨窗递增退避；
	// 403 / 网络错误保持短抖动（0.8.21 实测 403 换一套签名立即重试通常就过，
	// 它不落在失败窗里）。
	emptyRetry := false
	// 0.8.24：**空正文两次就换备用域名**。
	//
	// 上游按域名做灰度：主域对某台设备持续回空正文时，备用域往往还是通的
	// （实测同一设备同一剧集，主域 0/3、备域能拿到完整 video_model）。
	// 以前死磕一个域名，空正文只能靠"等失败窗过去"，于是把 5 次重试
	// 全部耗在同一个不可用的入口上。
	activeBase := base
	usedFallback := false
	swappedIdentity := false
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			// 换维度优先于等待（见上面 emptyBudget 处的注释）。
			//
			// 参考项 kuoluoss/zhenguojian 的做法：候选矩阵按「模式 × 域名」
			// 排列，每试完两种组合就 `resetHongguoPlaybackIdentity()` 换一次
			// 身份，每次只留 250ms 间隔。实测这条路比"原地跨窗退避"快得多 ——
			// 空正文几乎都是「这个身份或这个域名被拒」，换掉就通，等没用。
			wait := providerQuickRetryDelay()
			if emptyRetry {
				if !usedFallback {
					activeBase = "https://" + hongguoAppFallbackHost
					usedFallback = true
				} else if !swappedIdentity {
					swappedIdentity = true
					activeBase = base
					if freshID := newHongguoDeviceID(); freshID != "" {
						deviceID = freshID
						query.Set("device_id", deviceID)
					}
				} else {
					// 域名和身份都换过了还空 → 才开始跨窗退避
					wait = hongguoAppRetryDelay(emptyCount)
				}
			}
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		emptyRetry = false
		request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(activeBase, "/")+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		request.Header.Set("User-Agent", hongguoAppUserAgent)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("X-XS-From-Web", "0")
		request.Header.Set("Sdk-Version", "2")
		// 0.8.11：弹幕走红果的**评论接口**，它用的是另一套签名
		// （X-Gorgon / X-Argus / X-Ladon），而且必须带这两个业务头，
		// 少一个就返回空列表。上下文里带 hongguoCommentKey 时切过去。
		var commentNonce hongguoCommentNonce
		if comment {
			commentNonce, err = newHongguoCommentNonce()
			if err != nil {
				return nil, errors.New("无法准备红果弹幕请求")
			}
			request.Header.Set("Comment-Source", "601")
			request.Header.Set("Server-Channel", "1000")
		}
		if payload != nil {
			request.Header.Set("Content-Type", "application/json; charset=utf-8")
		}
		// 0.8.23：不再有"最后两次退回裸请求"。探针实测（n=20）裸请求
		// **全部**返回空正文 —— 签名头是服务端的准入条件，去掉它不是
		// "换个方式试试"，而是把两次预算白白扔进必然失败。
		response, err := downloader.doPreparedCatalogRequest(request, 20*time.Second, func(prepared *http.Request) {
			now := time.Now()
			query.Set("_rticket", strconv.FormatInt(now.UnixMilli(), 10))
			prepared.URL.RawQuery = query.Encode()
			if comment {
				signHongguoCommentRequest(prepared, commentNonce, now)
			} else {
				signHongguoAppRequest(prepared, body, prepared.URL.RawQuery, deviceID, now)
			}
		})
		if err != nil {
			lastErr = err
			var backoff *requestBackoff
			if errors.As(err, &backoff) {
				// 之前某个请求留下的 host 退避会在这里把重试直接挡掉，而红果
				// App 接口的退避本来就不可信（见 skipCatalogObserveKey）。
				// 就地清掉这一条，继续用新签名重试。
				if downloader.limiter != nil {
					downloader.limiter.clearBackoff(backoff.host)
				}
				continue
			}
			if ctx.Err() != nil {
				return nil, err
			}
			continue
		}
		content, readErr := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
		response.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if response.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("红果 App 接口 HTTP %d", response.StatusCode)
			// 4xx 不能一概放弃：红果对**签名失配**回的就是 403，换一套随机签名
			// 重试通常就过。只有 429（真的被限流）继续硬打才会更糟。
			if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests {
				continue
			}
			return nil, lastErr
		}
		if len(content) == 0 {
			// 这是"随机空响应"，属于可重试的临时状态，不能当作终态错误。
			// 0.8.16 第四批：单列成哨兵值 —— 上层据此换设备重注册（空正文
			// 最常见的原因就是服务端不认当前 device_id）。
			lastErr = errHongguoAppEmptyResponse
			emptyCount++
			emptyRetry = true
			if emptyCount >= emptyBudget {
				return nil, lastErr
			}
			continue
		}
		if len(content) > providerMaxBodyBytes {
			return nil, errors.New("红果 App 接口返回的数据过大")
		}
		var result map[string]any
		decoder := json.NewDecoder(bytes.NewReader(content))
		decoder.UseNumber()
		if err := decoder.Decode(&result); err != nil || result == nil {
			lastErr = errors.New("红果 App 接口返回格式异常")
			continue
		}
		code := firstNonEmpty(mapString(result, "code", "Code", "status_code"), mapString(nestedMap(result, "BaseResp"), "StatusCode"))
		if code != "" && code != "0" {
			// 非 0 的 code 是**真的业务结果**（会话失效、内容下架、被限流），
			// 不是签名失配 —— 签名那一发失败时服务端回的是 403 或空正文，
			// 那两条已经在上面重试过了。所以这里不重试：重试既救不回来，又会
			// 让目录侧"丢掉失效会话再重来"这条恢复路径多绕两圈。
			return nil, fmt.Errorf("红果 App 接口暂不可用（%s）", truncate(code, 20))
		}
		return result, nil
	}
	return nil, lastErr
}
