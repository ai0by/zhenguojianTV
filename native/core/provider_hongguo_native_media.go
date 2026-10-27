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
func (downloader *Downloader) resolveHongguoAppMedia(ctx context.Context, videoID string) (providerMedia, error) {
	if !hongguoNumericID.MatchString(videoID) {
		return providerMedia{}, errors.New("红果视频 ID 无效")
	}
	// 0.8.14：取流接口只认服务端注册过的设备。本地随机生成的 device_id 会被
	// 静默拒绝（200 + 空正文），所以先保证设备已注册（见 provider_hongguo_device.go）。
	// 注册失败不阻断：退回旧设备继续试，让上层错误信息带上真实原因。
	deviceErr := downloader.ensureHongguoDevice(ctx)
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
		// 注册失败时取流必然失败（空正文），把根因放在最前面，
		// 别让用户看到"接口返回空响应"这种没有指向性的说法。
		if deviceErr != nil {
			return providerMedia{}, fmt.Errorf("%w：%v", errHongguoDeviceUnregistered, deviceErr)
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
		// 空正文最常见的原因是设备没注册成功（服务端不认这个 device_id）。
		if deviceErr != nil {
			return providerMedia{}, fmt.Errorf("%w：%v", errHongguoDeviceUnregistered, deviceErr)
		}
		return providerMedia{}, errors.New("红果 App 未返回该集播放信息")
	}
	// 0.8.14：`video_model` 自己就带了 video_list 与 video_duration，
	// 先用它，省掉 fallback_api 那一跳网络请求（也少一处失败可能）。
	if media, err := selectHongguoAppVideoList(model); err == nil {
		if err := hongguoRejectPreview(media); err != nil {
			return providerMedia{}, err
		}
		return media, nil
	}
	fallback := hongguoFallbackAddress(model["fallback_api"])
	if fallback == "" {
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
	return media, nil
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
	duration, _ := strconv.ParseFloat(mapString(info, "video_duration", "duration"), 64)
	for _, row := range rows {
		meta := nestedMap(row, "video_meta")
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
