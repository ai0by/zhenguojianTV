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
	"sort"
	"strconv"
	"strings"
	"time"
)

// 红果 App 取流分两步（0.8.27 起第 1 步走播放专用链路）：
//  1. 每轮注册新身份，首选 GET /reading/bookapi/video_tab/multi_video_model/v1/
//     拿到本集的 video_model；失败才退回旧 POST 主/备域，见
//     provider_hongguo_playback_routes.go。
//  2. GET fallback_api 拿到 video_info.data，内含 video_list（各清晰度）与 key_seed
//     —— 只在 video_model 自带清单是试看片段时才需要。
//
// video_list 里的 main_url / backup_url 是 spade 密文，需用 key_seed 解密；
// 每个清晰度的 spade_a 是 CENC 内容密钥。六头签名由播放侧单发请求自己处理
// （x-gorgon/x-argus/x-ladon/x-helios/x-medusa），这里只负责业务参数。
// errHongguoAppEmptyModel 表示取流接口没给出 video_model。
//
// 两种形态都归到这里：`200 + 空正文`（最常见，上游按身份静默屏蔽）和
// "有 JSON 但没有 video_model"。单列成哨兵值，是为了让上层统一标记成
// media_transient，让前端静默再试一次。
var errHongguoAppEmptyModel = errors.New("红果 App 未返回该集播放信息")

// errHongguoAppEmptyResponse 表示接口回了 200 但正文是空的。
//
// 这是"服务端不认当前设备"的典型表现，也是取流失败里占比最高的一种。
var errHongguoAppEmptyResponse = errors.New("红果 App 接口返回空响应")

// hongguoSingleflightRetryKey 标记「已经因为共享取流失败自己重取过一次」。
// 只用来防递归，不参与业务逻辑；见 resolveHongguoAppMedia 的等待分支。
type hongguoSingleflightRetryKey struct{}

// hongguoAppProbeLimit 是单次取流最多探测几个清晰度档位。
//
// 探测是为了识破"元数据说 79 秒、实际只给 10 秒"的试看片源 —— 这种档位
// 的 video_duration 跟正片一模一样，只有真读文件头才看得出来。正常情况下
// 第一档就能过，留 3 档只是给"最高清那档被替换成试看"留退路。
const hongguoAppProbeLimit = 3

// hongguoAppProbeTimeout 是单次时长探测的超时。
//
// 只读 64KB 头部，CDN 上通常几十毫秒。给 4 秒是为了容忍移动网络抖动，
// 同时又不至于让"探测"本身变成用户等待的大头。
const hongguoAppProbeTimeout = 4 * time.Second

// resolveHongguoAppMedia 取一集的片源（0.8.27 整条重写）。
//
// 旧实现走共用的 hongguoAppRequest（25s 预算 / 5 次空正文 / 跨窗退避），
// 外面还叠了「原地重取 → 重注册设备 → 再取」三层，实测 p50 12.49s、
// p95 17.59s。当时误判成「一个注册身份只能成功取流一次」，0.8.29 已推翻：
// 真因是上游单独收紧了取流接口的签名校验，通用 App 签名一律回 200 + 0 字节，
// 旧链路偶尔能出片只是签名碰巧命中。身份轮换保留下来只作容错，不再是必需。
//
// 现在每轮主动注册新身份，按 GET tab → 旧 POST 主域 → 旧 POST 备域的顺序
// 单发即换，全失败才换一个全新身份重试首选路径一次。取流阶段硬预算 6 秒
// （只在全失败时才可能跑满）；拿到 model 之后的试看探测用父 ctx，不受这个
// 预算影响。
func (downloader *Downloader) resolveHongguoAppMedia(ctx context.Context, videoID string) (media providerMedia, err error) {
	if !hongguoNumericID.MatchString(videoID) {
		return providerMedia{}, errors.New("红果视频 ID 无效")
	}
	// 命中短缓存就不发任何请求、也不注册新身份（少一次注册 RTT）。
	if cached, ok := downloader.hongguoMediaCacheGet(videoID); ok {
		return cached, nil
	}
	// 0.8.28 并发合并（singleflight）：预载（session 是 preload-<集号>）与前台
	// 正片很可能同时 miss 成功缓存，若各自独立取流，就会对同一 videoID 各
	// 注册一次新身份 —— 重复注册既白白消耗
	// log.snssdk.com/service/2/device_register/，又把两发请求一起压进
	// limiter 队列（正是连播卡顿的放大器）。把同一集的并发取流合并成一发，
	// 后来者等前者的结果即可。
	//
	// 用独立的 inflightMu，不与 hongguoMediaCacheGet/Put 的 client.mu 嵌套，
	// 避免两把锁互相等待造成死锁。**失败结果不共享**：leader 很可能是被切集
	// cancel 掉的预载（session 是 preload-<集号>），把它的 context.Canceled
	// 原样交给前台正片会平白判死一集；与 mediaCache「失败不落缓存」同语义。
	client := downloader.hongguoClient()
	client.inflightMu.Lock()
	if call := client.inflight[videoID]; call != nil {
		client.inflightMu.Unlock()
		select {
		case <-call.done:
			if call.err != nil &&
				ctx.Err() == nil &&
				ctx.Value(hongguoSingleflightRetryKey{}) == nil {
				// leader 的 defer 已经在 close(done) 之前删掉登记，这里回来
				// 要么自己成为新 leader，要么等到另一个新 leader，不会死循环。
				// ctx value 限定只重取一次，避免上游持续失败时请求数翻倍。
				return downloader.resolveHongguoAppMedia(
					context.WithValue(ctx, hongguoSingleflightRetryKey{}, true),
					videoID,
				)
			}
			return call.media, call.err
		case <-ctx.Done():
			return providerMedia{}, ctx.Err()
		}
	}
	call := &hongguoInflightCall{done: make(chan struct{})}
	if client.inflight == nil {
		client.inflight = map[string]*hongguoInflightCall{}
	}
	client.inflight[videoID] = call
	client.inflightMu.Unlock()
	// 只有"登记了自己"的这一发负责写回结果、关 done、删登记；等待方从不写
	// map，也就不会误删别人的登记。命名返回值让 defer 能拿到最终结果。
	defer func() {
		client.inflightMu.Lock()
		call.media, call.err = media, err
		if client.inflight[videoID] == call {
			delete(client.inflight, videoID)
		}
		close(call.done)
		client.inflightMu.Unlock()
	}()
	// 红果 App 接口的 4xx 基本是签名随机失配，不是站点封禁，不参与 host 封禁统计。
	ctx = context.WithValue(ctx, skipCatalogObserveKey{}, true)
	// 播放取流打上最高优先级：不再被后台弹幕预取/预载/目录请求排队拖死
	// （详见 network_limiter.go 的 playbackPriorityKey 说明）。子 ctx 的
	// 设备注册、3 条取流路由、CDN 头部探测都会继承这个标记。
	ctx = withPlaybackPriority(ctx)
	fetchCtx, cancel := context.WithTimeout(ctx, hongguoPlaybackFetchBudget)
	defer cancel()

	routes := hongguoPlaybackRoutes(videoID)
	var model map[string]any
	var lastErr error
	var registerErr error
	// 记下成功取到 model 那一发所用的身份：后面编码救济要用同一个已注册
	// 身份，不能退回可能为空的目录侧身份（见 hongguoPlaybackModelV1 注释）。
	var playbackIdentity hongguoPlaybackIdentity
	for round := 0; round < 2 && model == nil && fetchCtx.Err() == nil; round++ {
		identity := downloader.hongguoFallbackPlaybackIdentity()
		// 每一轮都必须注册一个全新身份。0.8.27 构建前复测的 11/12 里，唯一
		// 那次失败是注册抖动超时：旧逻辑在第二轮因为「第一轮没注册成功」就
		// 跳过重注册，拿目录侧旧身份单发（按身份单次有效的机制必然空正文），
		// 最后硬报设备注册失败。注册抖动本身是暂态，给它第二次机会就能自愈。
		if fresh, err := downloader.hongguoFreshPlaybackIdentity(fetchCtx); err == nil {
			identity = fresh
			registerErr = nil
		} else {
			registerErr = err
		}
		roundRoutes := routes
		if round > 0 {
			roundRoutes = routes[:1]
		}
		for _, route := range roundRoutes {
			if fetchCtx.Err() != nil {
				break
			}
			candidate, err := downloader.hongguoPlaybackModelOnce(fetchCtx, route, videoID, identity)
			if err == nil {
				model = candidate
				playbackIdentity = identity
				break
			}
			lastErr = err
		}
		if model == nil && round == 0 {
			// 一档短等待后换全新身份再试，不拉长退避、不叠加秒级等待。
			select {
			case <-time.After(150 * time.Millisecond):
			case <-fetchCtx.Done():
			}
		}
	}
	if model == nil {
		if ctx.Err() != nil {
			return providerMedia{}, ctx.Err()
		}
		// 取流预算跑完是网络抖动，不该硬判成「设备注册失败」：收敛成
		// transient，让前端静默再试一次，用户点一下重试通常就跨出失败窗。
		if fetchCtx.Err() != nil {
			// 吞成空正文之前先留一条可诊断信息：预算是被排队/注册抖动吃光的，
			// 光看「未返回播放信息」根本分不清是上游拒绝还是本地 limiter 拥塞。
			// recordDiagnostic 在没有 diagnostics 时是空操作，不引入新日志库。
			downloader.recordDiagnostic(diagnosticEvent{
				Event:   "hongguo_app_playback_budget_exhausted",
				Source:  sourceHongguo,
				Message: fmt.Sprintf("取流预算耗尽（videoID=%s）：%v；末次注册错误=%v；末次路由错误=%v", videoID, fetchCtx.Err(), registerErr, lastErr),
			})
			return providerMedia{}, errHongguoAppEmptyModel
		}
		// 新身份是这次修复的前提。两轮注册都没成功时，继续走公开片源只会白等
		// 两个往返还把真正的根因埋掉 —— 直接把注册失败抛给上层短路。
		if registerErr != nil {
			return providerMedia{}, fmt.Errorf("%w：%v", errHongguoDeviceUnregistered, registerErr)
		}
		if lastErr == nil {
			return providerMedia{}, errHongguoAppEmptyModel
		}
		// 空正文 / 无 model 统一收敛成哨兵，上层据此标记 media_transient。
		if errors.Is(lastErr, errHongguoAppEmptyModel) {
			return providerMedia{}, errHongguoAppEmptyModel
		}
		return providerMedia{}, lastErr
	}
	// 2026-11-12：GET video_tab 对少数集**只**返回 bytevc2（片源盒里
	// frma=bvc2，FFmpeg / libmpv 没有对应解码器），用户侧表现为「取流成功、
	// 底部时长正常、但一直播不出来」，重启 App 也不会好 —— 这是上游按集
	// 给的编码数据，不是进程内状态。往后跳到 bytevc1 / 明文的那几集又能播。
	//
	// 实测同一 video_id 用 POST /novel/player/video_model/v1/ 能拿到
	// h265_hvc1 档位（相同的 CENC key、相同的时长、片源盒为 encv+hvcC+cenc），
	// 20/20 集走生产 hongguoMediaFromModel 全链路通过，单次 0.2~0.6s。
	//
	// 只在「明确只给了 bytevc2」时补这一跳：绝大多数集 GET tab 直接命中，
	// 零额外请求；救济失败也保留原 model，行为不比修复前更差。
	if hongguoModelNeedsCodecRescue(model) {
		// 单独给预算：fetchCtx 可能已被前两轮注册 / 路由吃光。父 ctx 仍保留
		// playbackPriority 与取消语义，救济超时也只是保留原 model。
		rescueCtx, stopRescue := context.WithTimeout(ctx, hongguoPlaybackRescueBudget)
		rescued, rescueErr := downloader.hongguoPlaybackModelV1(rescueCtx, videoID, playbackIdentity)
		stopRescue()
		if rescueErr == nil && !hongguoModelNeedsCodecRescue(rescued) {
			downloader.recordDiagnostic(diagnosticEvent{
				Event:   "hongguo_app_playback_codec_rescued",
				Source:  sourceHongguo,
				Message: fmt.Sprintf("videoID=%s：video_tab 只给 bytevc2，已切到 video_model/v1 的 hvc1 档位", videoID),
			})
			model = rescued
		}
	}
	media, err = downloader.hongguoMediaFromModel(ctx, model)
	if err != nil {
		return providerMedia{}, err
	}
	downloader.hongguoMediaCachePut(videoID, media)
	return media, nil
}

// hongguoMediaFromModel 把接口给的 video_model 变成可播地址（不再发取流请求）。
//
// 0.8.14：`video_model` 自己就带了 video_list 与 video_duration，先用它，
// 省掉 fallback_api 那一跳网络请求（也少一处失败可能）。
// 0.8.16 第四批：`video_model` 里只给试看片段时，别急着报错 ——
// 再走一次 fallback_api，两处数据不一定同时降级。
func (downloader *Downloader) hongguoMediaFromModel(ctx context.Context, model map[string]any) (providerMedia, error) {
	var previewErr error
	if media, err := selectHongguoAppVideoList(model); err == nil {
		if rejectErr := hongguoRejectPreview(media); rejectErr == nil {
			return downloader.verifyHongguoAppMedia(ctx, media)
		} else {
			previewErr = rejectErr
		}
	}
	fallback := hongguoFallbackAddress(model["fallback_api"])
	if fallback == "" {
		if previewErr != nil {
			return providerMedia{}, previewErr
		}
		return providerMedia{}, errors.New("红果 App 未返回该集片源地址")
	}
	outer, err := downloader.hongguoAppJSON(ctx, fallback)
	if err != nil {
		return providerMedia{}, err
	}
	if code := firstNonEmpty(mapString(outer, "Code", "code"), mapString(nestedMap(outer, "video_info"), "Code", "code")); code != "" && code != "0" {
		return providerMedia{}, fmt.Errorf("红果 App 片源接口暂不可用（%s）", truncate(code, 20))
	}
	info := nestedMap(outer, "video_info", "data")
	if len(info) == 0 {
		return providerMedia{}, errors.New("红果 App 未返回该集片源清单")
	}
	media, err := selectHongguoAppVideoList(info)
	if err != nil {
		return providerMedia{}, err
	}
	if err := hongguoRejectPreview(media); err != nil {
		return providerMedia{}, err
	}
	return downloader.verifyHongguoAppMedia(ctx, media)
}

// verifyHongguoAppMedia 校验 App 取到的地址是不是真能当正片播（0.8.16 第四批）。
//
// 为什么光看 `video_duration` 不够：部分设备/出口拿到的是**试看片段**，
// 但 `video_duration` 报的仍是整集时长（79 秒），地址指向的文件本身只有
// 10 秒 —— 元数据完全对得上，只有真读文件头才露馅。用户侧表现就是
// "同一部剧这台手机只能看 10 秒，那台正常"。
//
// 策略：
//   - 依次探测前几档（最高清优先），第一档不是预告就用它；
//   - 某一档是预告就换下一档（可能只有 1080p 被替换成试看）；
//   - **探测失败就放行**：地址来自官方接口、带 CENC 密钥，可信度远高于
//     公开片源；不能因为一次 4 秒超时就把正常剧集判死。
func (downloader *Downloader) verifyHongguoAppMedia(ctx context.Context, media providerMedia) (providerMedia, error) {
	candidates := make([]providerMedia, 0, 1+len(media.Variants))
	candidates = append(candidates, media)
	for _, variant := range media.Variants {
		if variant.URL != media.URL {
			candidates = append(candidates, variant)
		}
	}
	if len(candidates) > hongguoAppProbeLimit {
		candidates = candidates[:hongguoAppProbeLimit]
	}
	var shortest time.Duration
	for _, candidate := range candidates {
		duration, err := downloader.hongguoProbeMediaDuration(ctx, candidate)
		if err != nil {
			if ctx.Err() != nil {
				return providerMedia{}, ctx.Err()
			}
			// 探测不出来 → 当作可信，放行（见上面的说明）。
			chosen := candidate
			chosen.Variants = media.Variants
			return chosen, nil
		}
		if !hongguoLooksLikePreview(duration) {
			chosen := candidate
			// 用实测时长覆盖元数据：进度条 / 自动下一集都更准。
			chosen.Duration = duration
			chosen.Variants = media.Variants
			return chosen, nil
		}
		if shortest == 0 || duration < shortest {
			shortest = duration
		}
	}
	if shortest == 0 {
		shortest = hongguoPreviewMaxDuration
	}
	return providerMedia{}, fmt.Errorf("红果只返回了 %.0f 秒试看片段，未取得正片", shortest.Seconds())
}

// hongguoProbeMediaDuration 读片源头部 64KB 判断真实时长。
//
// 红果的 mp4 把 moov 放在最前面（ftyp 32B + moov 11441B），一次 Range
// 请求足够。CENC 加密只作用于 mdat 里的样本，moov/mvhd 是明文。
func (downloader *Downloader) hongguoProbeMediaDuration(ctx context.Context, media providerMedia) (time.Duration, error) {
	if !isProviderHTTPMediaURL(media.URL) {
		return 0, errors.New("片源地址无效")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, media.URL, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("User-Agent", hongguoAppUserAgent)
	if media.Referer != "" {
		request.Header.Set("Referer", media.Referer)
	}
	request.Header.Set("Range", "bytes=0-65535")
	response, err := downloader.doCatalogRequestWithTimeout(request, hongguoAppProbeTimeout)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPartialContent && response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("片源返回 HTTP %d", response.StatusCode)
	}
	prefix, readErr := io.ReadAll(io.LimitReader(response.Body, hongguoMediaProbeBytes))
	if readErr != nil {
		return 0, readErr
	}
	return hongguoMediaDurationFromPrefix(prefix)
}

// hongguoRejectPreview 挡掉"只给预告"的片源（0.8.14）。
//
// 红果短剧单集 60~120 秒，预告固定 10 秒；`video_duration` 明显偏短时
// 说明官方只给了预告，宁可报错也不要让用户以为能看全集。
func hongguoRejectPreview(media providerMedia) error {
	if hongguoLooksLikePreview(media.Duration) {
		return fmt.Errorf("红果只返回了 %.0f 秒预告，未取得正片", media.Duration.Seconds())
	}
	return nil
}

// hongguoPlayerQuery 补齐真实客户端取流时带的 query 参数（0.8.14）。
//
// 这些参数只有播放器侧的接口会用到，目录 / 详情接口不需要，所以只在
// 取流时附加。注意：补齐之后取流接口对游客**仍然大多返回 200 + 空正文**
// （2026-10-27 实测 16 次全空），空正文是上游"静默拒绝"的通用表现 ——
// 不存在的路由会返回 404，说明接口还在，只是不给游客正片。
// 保留这组参数是为了尽量贴近真实客户端，不是解决问题的关键。
func hongguoPlayerQuery() url.Values {
	return url.Values{
		"channel":  {"oppo_8662_64"},
		"host_abi": {"arm64-v8a"},
		// 0.8.24：`dragon_device_type` 保持 pad 不动。
		//
		// 曾怀疑它与 is_android_pad_screen=0 矛盾导致取流被拒，实测切到
		// phone 与保持 pad **都是 5/5**，所以它不是根因。别为它去改 UA 和
		// resolution —— 这几个字段必须配套，单改一个反而更容易被识别。
		"dragon_device_type":      {"pad"},
		"pv_player":               {hongguoAppVersionCode},
		"compliance_status":       {"0"},
		"need_personal_recommend": {"1"},
		"player_so_load":          {"1"},
		"is_android_pad_screen":   {"0"},
		"rom_version":             {"BP2A.250605.031.A3+release-keys"},
	}
}

// hongguoFallbackAddress 兼容 fallback_api 的三种形态：
// 纯字符串、{fallback_api: "..."} 对象、字符串数组。
func hongguoFallbackAddress(value any) string {
	switch typed := value.(type) {
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return ""
		}
		if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
			var nested map[string]any
			decoder := json.NewDecoder(strings.NewReader(text))
			decoder.UseNumber()
			if decoder.Decode(&nested) == nil {
				return hongguoFallbackAddress(nested["fallback_api"])
			}
			return ""
		}
		return text
	case map[string]any:
		return hongguoFallbackAddress(typed["fallback_api"])
	case []any:
		if len(typed) > 0 {
			return hongguoFallbackAddress(typed[0])
		}
	}
	return ""
}

func (downloader *Downloader) hongguoAppJSON(ctx context.Context, rawURL string) (map[string]any, error) {
	rawURL = strings.TrimSpace(rawURL)
	if !isProviderHTTPMediaURL(rawURL) || !hongguoSpadeHostAllowed(rawURL) {
		return nil, errors.New("红果 App 片源接口地址无效")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", hongguoAppUserAgent)
	request.Header.Set("Accept", "application/json")
	response, err := downloader.doCatalogRequestWithTimeout(request, 20*time.Second)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	content, readErr := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
	if readErr != nil || len(content) == 0 || len(content) > providerMaxBodyBytes {
		return nil, errors.New("红果 App 片源接口未返回有效数据")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("红果 App 片源接口 HTTP %d", response.StatusCode)
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil || result == nil {
		return nil, errors.New("红果 App 片源接口返回格式异常")
	}
	return result, nil
}

// selectHongguoAppVideoList 从片源清单里挑一条可用的播放地址。
//
// 排序策略沿用产品既有约定：**分辨率优先，同分辨率下编码更兼容者优先**
// （h264 > hevc/bytevc1 > bytevc2）。bytevc2 属于新编码，多数 TV 播放器
// 无法解码，因此只在没有任何其它可用档位时才作为兜底，避免出现
// "整部剧一集都播不了"。
// hongguoVideoSeed 取解密种子（0.8.14）。
//
// `fallback_api` 那条链路的 key_seed 在 video_info.data 顶层；
// 而 `video_model` 自己带 video_list 时，key_seed 有可能被放进
// video_info / data / encrypt_info 里。这里按几个已知落点依次找，
// 找不到就返回空串（调用方会当成"地址是明文"处理，不会崩）。
func hongguoVideoSeed(info map[string]any) string {
	if seed := mapString(info, "key_seed"); seed != "" {
		return seed
	}
	for _, path := range [][]string{
		{"video_info", "data"},
		{"video_info"},
		{"data"},
		{"video_model"},
	} {
		if seed := mapString(nestedMap(info, path...), "key_seed"); seed != "" {
			return seed
		}
	}
	return ""
}

func selectHongguoAppVideoList(info map[string]any) (providerMedia, error) {
	seed, _ := decodeHongguoBase64(hongguoVideoSeed(info))
	rows := hongguoVideoRows(info["video_list"])
	if len(rows) == 0 {
		return providerMedia{}, errors.New("红果 App 未返回可用清晰度")
	}

	type candidate struct {
		media  providerMedia
		rank   int
		height int
	}
	var all []candidate
	baseDuration, _ := strconv.ParseFloat(mapString(info, "video_duration", "duration"), 64)
	for _, row := range rows {
		meta := nestedMap(row, "video_meta")
		// 0.8.16 第四批：优先用**该档自带**的时长。
		//
		// 部分设备拿到的清单里只有个别清晰度被换成了试看片段（10 秒），
		// 而顶层 video_duration 仍是整集时长。只看顶层就会把试看档当正片
		// 选中，播到 10 秒就断。
		duration := baseDuration
		if value, err := strconv.ParseFloat(mapString(row, "duration", "video_duration"), 64); err == nil && value > 0 {
			duration = value
		} else if value, err := strconv.ParseFloat(mapString(meta, "duration", "video_duration"), 64); err == nil && value > 0 {
			duration = value
		}
		codec := strings.ToLower(firstNonEmpty(mapString(meta, "codec_type"), mapString(row, "codec_type"), mapString(row, "gear_des_key")))
		rank := hongguoCodecRank(codec)
		spade := firstNonEmpty(mapString(row, "spade_a"), mapString(nestedMap(row, "encrypt_info"), "spade_a"))
		var contentKey []byte
		if spade != "" {
			key, err := hongguoContentKey(spade)
			if err != nil {
				continue
			}
			contentKey = key
		}
		height := hongguoDefinitionHeight(meta, row)
		for _, address := range hongguoAppAddresses(row, seed) {
			all = append(all, candidate{
				media: providerMedia{
					URL: address, Referer: "", Duration: time.Duration(duration * float64(time.Second)),
					CENCKey: contentKey, Quality: height,
				},
				rank: rank, height: height,
			})
		}
	}
	if len(all) == 0 {
		return providerMedia{}, errors.New("红果 App 未返回可解析的片源地址")
	}
	// 优先排除 bytevc2；只有在它之外一无所获时才回退到全量候选。
	usable := make([]candidate, 0, len(all))
	for _, item := range all {
		if item.rank >= 2 {
			usable = append(usable, item)
		}
	}
	if len(usable) == 0 {
		usable = all
	}
	// 0.8.16 第四批：标称时长只有十几秒的档位是试看片段，先剔掉。
	// 剔完一个不剩就保留原样，交给后面的时长校验报错（而不是"没有清晰度"）。
	playable := make([]candidate, 0, len(usable))
	for _, item := range usable {
		if hongguoLooksLikePreview(item.media.Duration) {
			continue
		}
		playable = append(playable, item)
	}
	if len(playable) > 0 {
		usable = playable
	}
	sort.SliceStable(usable, func(i, j int) bool {
		if usable[i].height != usable[j].height {
			return usable[i].height > usable[j].height
		}
		return usable[i].rank > usable[j].rank
	})
	selected := usable[0].media
	for _, item := range usable {
		variant := item.media
		variant.Variants = nil
		selected.Variants = append(selected.Variants, variant)
	}
	return selected, nil
}

// hongguoVideoRows 兼容 video_list 的两种形态：对象（video_1..video_n）与数组。
func hongguoVideoRows(value any) []map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		rows := make([]map[string]any, 0, len(keys))
		for _, key := range keys {
			if row, ok := typed[key].(map[string]any); ok && len(row) > 0 {
				rows = append(rows, row)
			}
		}
		return rows
	case []any:
		rows := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if row, ok := item.(map[string]any); ok && len(row) > 0 {
				rows = append(rows, row)
			}
		}
		return rows
	}
	return nil
}

func hongguoCodecRank(codec string) int {
	switch {
	case codec == "h264" || codec == "avc" || codec == "avc1":
		return 3
	case codec == "hevc" || codec == "h265" || codec == "hvc1" || codec == "hev1" || codec == "bytevc1":
		return 2
	case codec == "bytevc2":
		return 1
	default:
		return 0
	}
}

func hongguoDefinitionHeight(meta, row map[string]any) int {
	for _, source := range []map[string]any{meta, row} {
		if height, err := strconv.Atoi(mapString(source, "vheight", "height")); err == nil && height > 0 {
			return height
		}
		if definition, err := strconv.Atoi(hongguoQualityNumber.FindString(mapString(source, "definition", "quality_desc"))); err == nil && definition > 0 {
			return definition
		}
	}
	return 0
}

func hongguoAppAddresses(row map[string]any, seed []byte) []string {
	var addresses []string
	seen := map[string]bool{}
	add := func(value any) {
		text, ok := value.(string)
		if !ok {
			return
		}
		address := hongguoAppAddress(text, seed)
		if address == "" || !hongguoSpadeHostAllowed(address) || seen[address] {
			return
		}
		seen[address] = true
		addresses = append(addresses, address)
	}
	add(row["main_url"])
	add(row["backup_url"])
	for _, item := range anyList(row["backup_url_list"]) {
		add(item)
	}
	for _, item := range anyList(row["backup_urls"]) {
		add(item)
	}
	return addresses
}

// hongguoAppAddress 依次尝试：明文地址 → spade 密文 → base64 明文。
// 三种形态在真实响应和历史数据里都出现过，任一成立即可。
func hongguoAppAddress(value string, seed []byte) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if isProviderHTTPMediaURL(value) {
		return value
	}
	if address, err := hongguoDecryptSpadeURL(value, seed); err == nil {
		return address
	}
	if decoded, err := decodeHongguoBase64(value); err == nil {
		if address := strings.TrimSpace(string(decoded)); isProviderHTTPMediaURL(address) {
			return address
		}
	}
	return ""
}

func hongguoMediaAddresses(info map[string]any) []string {
	var addresses []string
	seen := map[string]bool{}
	var add func(any)
	add = func(value any) {
		switch value := value.(type) {
		case string:
			address := strings.TrimSpace(value)
			if len(address) > 8192 {
				return
			}
			if !isProviderHTTPMediaURL(address) {
				decoded, err := decodeHongguoBase64(address)
				if err != nil {
					return
				}
				address = strings.TrimSpace(string(decoded))
			}
			if isProviderHTTPMediaURL(address) && !seen[address] {
				seen[address] = true
				addresses = append(addresses, address)
			}
		case []any:
			for _, item := range value {
				add(item)
			}
		}
	}
	for _, key := range []string{"main_url", "backup_url", "backup_url_1", "backup_url_2", "backup_urls", "url_list"} {
		add(info[key])
	}
	return addresses
}
