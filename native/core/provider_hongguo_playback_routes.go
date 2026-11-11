package core

// 红果播放取流专用链路（0.8.27 建，2026-11-11 换签名修好）。
//
// 2026-11-11 的逐路由对照实测把 0.8.27 的结论彻底改写了：上游并不是
// 「一个注册身份只能成功取流一次」，而是**对取流接口单独收紧了签名校验**。
// 通用 App 签名（signHongguoAppRequest，目录 / 详情 / 弹幕仍在用）打到这里
// 一律回 200 + 0 字节空正文，换多少设备号、换哪条路由都救不回来；只有
// signHongguoPlayerRequest 的播放器专用签名能拿到完整 video_model。
//
// 实测（WSL 直连）：当前产品链路 0/5；换成「播放器签名 + 现有 GET video_tab
// + 现有解析链」后 5/5 拿到带 CENC key / 时长 / 多档清晰度的真实地址，
// Range 请求可达；固定同一身份连取 4 集 4/4 成功。
//
// 本文件的定位不变，但签名只允许走播放器那套：
//  1. 与共用的 hongguoAppRequest 分开，播放侧不打断目录 / 弹幕的会话；
//  2. 首选官方现行 GET /reading/bookapi/video_tab/multi_video_model/v1/；
//  3. 每条路径**单发即换**，没有跨窗退避、没有多轮空正文重试；
//  4. 全部失败才再注册一个新身份，只重试首选路径一次；
//  5. 成功结果短缓存 10 分钟，重复打开同一集直接返回。
//
// 「每轮注册新身份」源于 0.8.27 那个已证伪的假设，现在并非必需（同一身份
// 连取 4 集全成功），但保留不影响正确性，且隔离目录侧身份仍有价值。清理它
// 属于独立优化，别和签名修复混在一次改动里。
//
// ⚠️ 取流请求**绝不能**再叠 X-Medusa / X-Helios：叠加会把播放器签名打回
// 空正文。那两项只属于通用签名（provider_hongguo_sign.go）。

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
	"time"
)

const (
	hongguoPlaybackTabPath    = "/reading/bookapi/video_tab/multi_video_model/v1/"
	hongguoPlaybackLegacyPath = "/novel/player/multi_video_model/v1/"

	// hongguoPlaybackFetchBudget 覆盖「注册 + 三条路由单发 + 一次新身份兜底」。
	// 实测一次注册约 0.3s、一发请求约 0.2s，正常命中 0.5~0.8s（复测 p50
	// 1.74s 含 CDN 头部探测）。0.8.27 构建前 12 集复测里出现过一次注册
	// 抖动把 3s 预算吃光：第二轮连重新注册都被跳过，直接硬报设备注册失败。
	// 放宽到 6s 只影响「全失败」的最坏等待，命中即走的中位耗时不变；相比
	// 旧链路 13~17s 的空转仍是量级改善。
	hongguoPlaybackFetchBudget = 6 * time.Second

	// 注册只是取流前的一跳，网络异常时不能让它吃掉整个取流预算；但也不能
	// 卡到 1.5s 就把偶发抖动判死 —— 注册接口本身还会内部重试 3 次。
	hongguoPlaybackRegisterBudget = 2500 * time.Millisecond
	hongguoPlaybackRequestTimeout = 3 * time.Second

	hongguoPlaybackCacheTTL   = 10 * time.Minute
	hongguoPlaybackCacheLimit = 64
)

// hongguoPlaybackIdentity 是**只供本轮取流使用**的匿名身份。
//
// 刻意不写回 hongguoCatalogState：目录 / 详情 / 弹幕有各自的分页游标与会话，
// 每播一集就换掉它们的 device_id 反而会打断那些链路。
// 2026-11-11 实测固定同一身份连取 4 集 4/4 成功，所以「一个身份只用一次」
// 不是服务端约束；每轮换身份只是保留下来的容错，不是必要条件。
type hongguoPlaybackIdentity struct {
	DeviceID  string
	InstallID string
}

type hongguoMediaCacheEntry struct {
	media   providerMedia
	expires time.Time
}

type hongguoPlaybackRoute struct {
	name   string
	host   string
	method string
	path   string
	extra  url.Values
	body   any
}

// hongguoAppBaseQuery 是目录与播放共用的基础 query。
//
// 抽成函数是为了让播放侧单发请求与 hongguoAppRequest 的签名上下文严格一致
// （签名覆盖最终 URL，query 差一个字段就会整发被拒）。
func hongguoAppBaseQuery(deviceID, installID string) url.Values {
	return url.Values{
		"aid": {"8662"}, "app_name": {"novelread"}, "version_code": {hongguoAppVersionCode}, "version_name": {hongguoAppVersionName},
		"manifest_version_code": {hongguoAppVersionCode}, "update_version_code": {hongguoAppVersionCode}, "channel": {"update_64"},
		"device_platform": {"android"}, "os": {"android"}, "ssmix": {"a"}, "device_type": {"25053RT47C"},
		"device_brand": {"Redmi"}, "language": {"zh"}, "os_api": {"36"}, "os_version": {"16"},
		"resolution": {"1280*2772"}, "dpi": {"520"}, "ac": {"wifi"}, "device_id": {deviceID}, "iid": {installID},
	}
}

// hongguoVideoTabQuery 是官方 GET video_tab 的 9 个 snake_case 参数。
//
// 缺任意一个就返回 100103 PARAM_INVALID；video_platform 只认 1024，
// from_video_id 必须存在（可以为空串）。这组参数来自官方 GetVideoModelRequest，
// 与旧 POST 的 biz_param 不是同一套，别把两者混着加。
func hongguoVideoTabQuery(videoID string) url.Values {
	return url.Values{
		"video_id":                  {videoID},
		"video_id_type":             {"0"},
		"device_level":              {"1"},
		"need_all_video_definition": {"1"},
		"need_mp4_align":            {"0"},
		"use_os_player":             {"0"},
		"video_platform":            {"1024"},
		"source":                    {"0"},
		"from_video_id":             {""},
	}
}

func hongguoPlaybackLegacyBody(videoID string) map[string]any {
	return map[string]any{
		"biz_param": map[string]any{
			"detail_page_version":       0,
			"device_level":              3,
			"disable_digg_stat":         false,
			"need_all_video_definition": true,
			"need_mp4_align":            false,
			"use_os_player":             false,
			"use_server_dns":            false,
			"video_platform":            1024,
		},
		"mixed_video_id_map": map[string]any{"1004": []string{videoID}},
	}
}

// hongguoPlaybackRoutes 按实测命中率排序：GET tab > 旧 POST 主域 > 旧 POST 备域。
//
// 首选 GET tab 已在「播放器专用签名」下端到端复测 5/5；两条旧 POST 保留为
// 兼容兜底（0.8.27 通用签名时代实测约 88%），**换签名后未单独复测**，所以
// 只作首选失败时的补位，不要调到前面去。
func hongguoPlaybackRoutes(videoID string) []hongguoPlaybackRoute {
	legacyExtra := hongguoPlayerQuery()
	legacyBody := hongguoPlaybackLegacyBody(videoID)
	return []hongguoPlaybackRoute{
		{name: "video_tab", host: hongguoAppBaseURL, method: http.MethodGet,
			path: hongguoPlaybackTabPath, extra: hongguoVideoTabQuery(videoID)},
		{name: "legacy_primary", host: hongguoAppBaseURL, method: http.MethodPost,
			path: hongguoPlaybackLegacyPath, extra: legacyExtra, body: legacyBody},
		{name: "legacy_backup", host: "https://" + hongguoAppFallbackHost, method: http.MethodPost,
			path: hongguoPlaybackLegacyPath, extra: legacyExtra, body: legacyBody},
	}
}

// hongguoFreshPlaybackIdentity 为这一轮取流注册一个新身份。
//
// 注册失败不阻断取流：退回目录侧那份身份继续试（错误信息由取流结果决定），
// 避免「注册接口偶发 SSL EOF」直接把播放判死。
func (downloader *Downloader) hongguoFreshPlaybackIdentity(ctx context.Context) (hongguoPlaybackIdentity, error) {
	registerCtx, cancel := context.WithTimeout(ctx, hongguoPlaybackRegisterBudget)
	defer cancel()
	registration, err := downloader.hongguoRegisterDevice(registerCtx)
	if err != nil {
		return hongguoPlaybackIdentity{}, err
	}
	return hongguoPlaybackIdentity{DeviceID: registration.DeviceID, InstallID: registration.InstallID}, nil
}

func (downloader *Downloader) hongguoFallbackPlaybackIdentity() hongguoPlaybackIdentity {
	client := downloader.hongguoClient()
	client.mu.Lock()
	defer client.mu.Unlock()
	return hongguoPlaybackIdentity{DeviceID: client.state.DeviceID, InstallID: client.state.InstallID}
}

// hongguoPlaybackModelOnce 发**一发**请求并返回 video_model，不做任何重试。
//
// 签名必须放在 doPreparedCatalogRequest 的 prepare 回调里：它会重设
// _rticket，先签名再改 URL 会让整发请求与签名对不上（0.8.24 踩过这个坑）。
func (downloader *Downloader) hongguoPlaybackModelOnce(
	ctx context.Context,
	route hongguoPlaybackRoute,
	videoID string,
	identity hongguoPlaybackIdentity,
) (map[string]any, error) {
	query := hongguoAppBaseQuery(identity.DeviceID, identity.InstallID)
	for key, values := range route.extra {
		query[key] = append([]string(nil), values...)
	}
	var body []byte
	if route.body != nil {
		encoded, err := json.Marshal(route.body)
		if err != nil {
			return nil, err
		}
		body = encoded
	}
	// 把"构造 request + prepare 签名 + 发送"收成一个闭包：命中 host 退避
	// 重发时必须重新构造 request（body 用新的 bytes.Reader、URL 重新拼），
	// 不能复用上一次那个已被 doPreparedCatalogRequest 改过 RawQuery 的
	// request。prepare 回调每次都会重设 _rticket 并重算签名，天然幂等。
	send := func() (*http.Response, error) {
		request, err := http.NewRequestWithContext(ctx, route.method, strings.TrimRight(route.host, "/")+route.path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		request.Header.Set("User-Agent", hongguoAppUserAgent)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("X-XS-From-Web", "0")
		request.Header.Set("Sdk-Version", "2")
		if route.body != nil {
			request.Header.Set("Content-Type", "application/json; charset=utf-8")
		}
		return downloader.doPreparedCatalogRequest(request, hongguoPlaybackRequestTimeout, func(prepared *http.Request) {
			now := time.Now()
			query.Set("_rticket", strconv.FormatInt(now.UnixMilli(), 10))
			prepared.URL.RawQuery = query.Encode()
			// 取流接口只认播放器专用签名；通用签名会拿到 200 + 0 字节。
			// 签名覆盖最终 RawQuery（含 _rticket），必须在重设之后调用。
			// 这里**不要**再调 signHongguoAppRequest：它会补上 X-Medusa /
			// X-Helios，把播放器签名整发打回空正文。
			signHongguoPlayerRequest(prepared, body, now)
		})
	}
	response, err := send()
	if err != nil {
		var backoff *requestBackoff
		if errors.As(err, &backoff) && downloader.limiter != nil {
			// 其他链路（目录/弹幕/预载）给这个 host 挂的退避会在 acquire
			// 阶段把取流直接挡下，而红果 App 的退避本来就不可信（见
			// skipCatalogObserveKey）。就地清掉这一条，用新签名原样重发
			// 一次；只重试一次，别把抖动放大成多轮等待。注册链路有自己的
			// 重试，不加这套。
			downloader.limiter.clearBackoff(backoff.host)
			response, err = send()
		}
	}
	if err != nil {
		return nil, err
	}
	content, readErr := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
	response.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("红果取流 %s HTTP %d", route.name, response.StatusCode)
	}
	if len(content) == 0 {
		return nil, errHongguoAppEmptyModel
	}
	if len(content) > providerMaxBodyBytes {
		return nil, errors.New("红果取流返回的数据过大")
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil || result == nil {
		return nil, errors.New("红果取流返回格式异常")
	}
	return hongguoPlaybackModelFromResult(result, videoID)
}

// hongguoPlaybackModelFromResult 兼容新旧两种响应容器：
//
//	旧 POST: data.<videoID>.video_model
//	GET tab: data.video_model_data.<videoID>.video_model（video_model 可能是字符串）
//
// 少数响应把 model 直接铺在 entry 上（带 video_list 但没有 video_model 键），
// 一并兼容。取不到 model 统一返回 errHongguoAppEmptyModel，由上层换路径。
func hongguoPlaybackModelFromResult(result map[string]any, videoID string) (map[string]any, error) {
	code := firstNonEmpty(mapString(result, "code", "Code", "status_code"), mapString(nestedMap(result, "BaseResp"), "StatusCode"))
	if code != "" && code != "0" {
		return nil, fmt.Errorf("红果取流暂不可用（%s）", truncate(code, 20))
	}
	data := nestedMap(result, "data")
	containers := []map[string]any{data, nestedMap(data, "video_model_data"), nestedMap(result, "video_model_data")}
	for _, container := range containers {
		if len(container) == 0 {
			continue
		}
		entry, _ := container[videoID].(map[string]any)
		if len(entry) == 0 && len(container) == 1 {
			for _, value := range container {
				if candidate, ok := value.(map[string]any); ok {
					entry = candidate
					break
				}
			}
		}
		if len(entry) == 0 {
			continue
		}
		model := hongguoVideoModelValue(entry["video_model"])
		if len(model) == 0 && len(hongguoVideoRows(entry["video_list"])) > 0 {
			model = entry
		}
		if len(model) > 0 {
			return model, nil
		}
	}
	return nil, errHongguoAppEmptyModel
}

func hongguoVideoModelValue(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		var model map[string]any
		decoder := json.NewDecoder(strings.NewReader(typed))
		decoder.UseNumber()
		if err := decoder.Decode(&model); err != nil {
			return nil
		}
		return model
	}
	return nil
}

// hongguoMediaCacheGet / Put 按 videoID 缓存最终片源（含所有清晰度 Variants）。
//
// 缓存在 hongguoClient 实例里而不是包级变量，避免跨用例泄漏；只缓存成功结果，
// 失败绝不缓存（用户紧接着切回来要能立刻重试）。
func (downloader *Downloader) hongguoMediaCacheGet(videoID string) (providerMedia, bool) {
	client := downloader.hongguoClient()
	client.mu.Lock()
	defer client.mu.Unlock()
	entry, ok := client.mediaCache[videoID]
	if !ok || time.Now().After(entry.expires) {
		if ok {
			delete(client.mediaCache, videoID)
		}
		return providerMedia{}, false
	}
	return entry.media, true
}

func (downloader *Downloader) hongguoMediaCachePut(videoID string, media providerMedia) {
	client := downloader.hongguoClient()
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.mediaCache == nil {
		client.mediaCache = map[string]hongguoMediaCacheEntry{}
	}
	now := time.Now()
	for key, entry := range client.mediaCache {
		if now.After(entry.expires) {
			delete(client.mediaCache, key)
		}
	}
	if len(client.mediaCache) >= hongguoPlaybackCacheLimit {
		oldestKey, oldest := "", time.Time{}
		for key, entry := range client.mediaCache {
			if oldest.IsZero() || entry.expires.Before(oldest) {
				oldest, oldestKey = entry.expires, key
			}
		}
		if oldestKey != "" {
			delete(client.mediaCache, oldestKey)
		}
	}
	client.mediaCache[videoID] = hongguoMediaCacheEntry{media: media, expires: now.Add(hongguoPlaybackCacheTTL)}
}
