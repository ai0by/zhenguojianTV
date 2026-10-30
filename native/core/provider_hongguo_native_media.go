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

// 红果 App 取流分两步：
//  1. POST /novel/player/multi_video_model/v1/ 拿到本集的 video_model
//     （多数情况下它自己就带 video_list，只有缺失时才需要第 2 步）；
//  2. GET fallback_api 拿到 video_info.data，内含 video_list（各清晰度）与 key_seed。
//
// video_list 里的 main_url / backup_url 是 spade 密文，需用 key_seed 解密；
// 每个清晰度的 spade_a 是 CENC 内容密钥。签名由 hongguoAppRequest 统一处理
// （x-gorgon/x-argus/x-ladon/x-helios/x-medusa），这里只负责业务参数。
// errHongguoAppEmptyModel 表示取流接口没给出 video_model。
//
// 两种形态都归到这里：`200 + 空正文`（errHongguoAppEmptyResponse，最常见，
// 服务端不认这个 device_id）和"有 JSON 但没有 video_model"。
// 单列成哨兵值，是为了让上层能区分"设备问题（可重注册自愈）"和"这集真的
// 没有地址（重注册也没用）"。
var errHongguoAppEmptyModel = errors.New("红果 App 未返回该集播放信息")

// errHongguoAppEmptyResponse 表示接口回了 200 但正文是空的。
//
// 这是"服务端不认当前设备"的典型表现，也是取流失败里占比最高的一种。
var errHongguoAppEmptyResponse = errors.New("红果 App 接口返回空响应")

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

func (downloader *Downloader) resolveHongguoAppMedia(ctx context.Context, videoID string) (providerMedia, error) {
	if !hongguoNumericID.MatchString(videoID) {
		return providerMedia{}, errors.New("红果视频 ID 无效")
	}
	// 0.8.14：取流接口只认服务端注册过的设备。本地随机生成的 device_id 会被
	// 静默拒绝（200 + 空正文），所以先保证设备已注册（见 provider_hongguo_device.go）。
	// 注册失败不阻断：退回旧设备继续试，让上层错误信息带上真实原因。
	deviceErr := downloader.ensureHongguoDevice(ctx)
	media, err := downloader.hongguoAppMediaOnce(ctx, videoID)
	if err == nil {
		return media, nil
	}
	if ctx.Err() != nil {
		return providerMedia{}, ctx.Err()
	}
	if errors.Is(err, errHongguoAppEmptyModel) && deviceErr == nil {
		// 0.8.16 第四批：**注册过 ≠ 服务端还认**。
		//
		// 设备号是首次启动时注册、然后落盘复用的。用户换网络、隔几天不打开、
		// 或者被风控降级之后，同一个 device_id 会重新变成"陌生设备"，取流
		// 又回到 200 + 空正文。旧代码这时把 Registered 当成"一定有效"，
		// 于是直接掉到公开片源 —— 而公开片源只给 10 秒预告，用户看到的就是
		// "这台手机每集只能播 10 秒"。
		//
		// 这里作废设备号、重新注册一次再取。成功就正常播；失败才往下走
		// 公开片源兜底（那条路现在会严格验时长，不会再把预告当正片）。
		if resetErr := downloader.resetHongguoDevice(ctx); resetErr == nil {
			if retried, retryErr := downloader.hongguoAppMediaOnce(ctx, videoID); retryErr == nil {
				return retried, nil
			} else if ctx.Err() != nil {
				return providerMedia{}, ctx.Err()
			}
		}
		return providerMedia{}, errHongguoAppEmptyModel
	}
	if deviceErr != nil {
		// 注册失败时取流必然失败（空正文），把根因放在最前面，
		// 别让用户看到"接口返回空响应"这种没有指向性的说法。
		return providerMedia{}, fmt.Errorf("%w：%v", errHongguoDeviceUnregistered, deviceErr)
	}
	return providerMedia{}, err
}

// hongguoAppMediaOnce 是"取一次流"的完整过程，不碰设备注册。
//
// 拆出来是为了让上层能在空正文时换设备重试一次（见 resolveHongguoAppMedia）。
func (downloader *Downloader) hongguoAppMediaOnce(ctx context.Context, videoID string) (providerMedia, error) {
	payload := map[string]any{
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
	result, err := downloader.hongguoAppRequest(ctx, http.MethodPost, "/novel/player/multi_video_model/v1/", hongguoPlayerQuery(), payload)
	if err != nil {
		if errors.Is(err, errHongguoAppEmptyResponse) {
			// 空正文 = 服务端不认这台设备，交给上层换设备重试一次。
			return providerMedia{}, fmt.Errorf("%w（%v）", errHongguoAppEmptyModel, err)
		}
		return providerMedia{}, err
	}
	entry, _ := nestedMap(result, "data")[videoID].(map[string]any)
	model, _ := entry["video_model"].(map[string]any)
	if encoded, ok := entry["video_model"].(string); ok && len(model) == 0 {
		decoder := json.NewDecoder(strings.NewReader(encoded))
		decoder.UseNumber()
		if err := decoder.Decode(&model); err != nil {
			return providerMedia{}, errors.New("红果 App 播放信息格式异常")
		}
	}
	if len(model) == 0 {
		return providerMedia{}, errHongguoAppEmptyModel
	}
	// 0.8.14：`video_model` 自己就带了 video_list 与 video_duration，
	// 先用它，省掉 fallback_api 那一跳网络请求（也少一处失败可能）。
	//
	// 0.8.16 第四批：`video_model` 里只给试看片段时，别急着报错 ——
	// 再走一次 fallback_api，两处数据不一定同时降级。
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
		"channel":                 {"oppo_8662_64"},
		"host_abi":                {"arm64-v8a"},
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
