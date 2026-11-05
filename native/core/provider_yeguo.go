package core

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 野果短剧当前是 Nuxt 3 站点：列表/详情通过 devalue payload 提供，
// API 响应用前端内置的 AES-128-CBC 配置加密。这里不把 key/iv 写死，
// 而是从当前 Nuxt chunk 动态发现并短时缓存，避免站点重新构建后失效。
const (
	sourceYeguo       = "yeguo"
	yeguoBaseURL      = "https://blue.ekzhadks.cc"
	yeguoAPIBaseURL   = "https://api.ekzhadks.cc"
	yeguoPageSize     = 20
	yeguoCryptoTTL    = 30 * time.Minute
	yeguoMaxChunkScan = 96
	// 密钥发现要抓 1 个首页 + 若干个 _nuxt chunk。站点正常时 3~6 秒内完成。
	//
	// 0.8.10：直取 + 跳过源级串行门之后，发现过程不会再被排队放大，
	// 所以超时从 40 秒收到 25 秒；失败后的负缓存也从 60 秒收到 20 秒——
	// 原来一旦某次发现失败，接下来整整一分钟里每次点播都直接报
	// 「站点当前不可用」，用户看到的就是"野果完全不能播"。
	yeguoCryptoDiscoverTimeout = 25 * time.Second
	yeguoCryptoRetryBackoff    = 20 * time.Second
)

// 0.8.9：原来还有 ygdj16.com 与 hoamqvgal.cc。实测这两个已不可用——
// ygdj16.com 一律返回反爬挑战页（<meta name="style-origin"> + Base64 脚本），
// hoamqvgal.cc 直接 EOF。四个域名一起竞速时，坏镜像有约一半概率"赢"，
// 密钥发现因此 3/5 失败，整个站源时好时坏。留两个能用的即可。
var yeguoBaseMirrors = []string{
	"https://blue.ekzhadks.cc",
	"https://yeguodj.com",
}

var (
	yeguoPayloadRefPattern = regexp.MustCompile(`(?i)([^"'<>\s]*_payload\.json(?:\?[^"'<>\s]*)?)`)
	yeguoScriptSrcPattern  = regexp.MustCompile(`(?is)<script\b[^>]*\bsrc=["']([^"']+\.js(?:\?[^"']*)?)["'][^>]*>`)
	// 0.8.9：站点把入口改成 importmap 之后，首页只有 1 个 `<script src>`，但整页
	// 通过 importmap / modulepreload / 预连接一共引用了约 28 个 `_nuxt/*.js`，
	// 加解密配置模块（325 字节，含 key/iv/sign_key）就在其中。只认 `<script src>`
	// 会连入口都找不到，BFS 直接空转 → 密钥发现失败。
	yeguoNuxtRefPattern    = regexp.MustCompile(`(/_nuxt/[A-Za-z0-9_\-]+\.js)`)
	yeguoJSRefPattern      = regexp.MustCompile(`["']([^"']+\.js(?:\?[^"']*)?)["']`)
	yeguoKeyWrappedPattern = regexp.MustCompile("(?i)\\bkey\\s*:\\s*[A-Za-z_$][A-Za-z0-9_$]*\\(\\s*[`\\\"']([^`\\\"']{16})[`\\\"']")
	yeguoKeyPattern        = regexp.MustCompile("(?i)\\bkey\\s*:\\s*[`\\\"']([^`\\\"']{16})[`\\\"']")
	yeguoIVWrappedPattern  = regexp.MustCompile("(?i)\\biv\\s*:\\s*[A-Za-z_$][A-Za-z0-9_$]*\\(\\s*[`\\\"']([^`\\\"']{16})[`\\\"']")
	yeguoIVPattern         = regexp.MustCompile("(?i)\\biv\\s*:\\s*[`\\\"']([^`\\\"']{16})[`\\\"']")
)

type yeguoCryptoConfig struct {
	Key string
	IV  string
}

type yeguoAPIEnvelope struct {
	Errcode any `json:"errcode"`
	Data    any `json:"data"`
	Message any `json:"message"`
	Msg     any `json:"msg"`
}

func yeguoMirrorHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, origin := range yeguoBaseMirrors {
		parsed, err := url.Parse(origin)
		if err == nil && strings.EqualFold(parsed.Hostname(), host) {
			return true
		}
	}
	return false
}

func yeguoDetailPath(sourceID string) string {
	return "/drama/detail/" + url.PathEscape(strings.TrimSpace(sourceID)) + "/"
}

func yeguoPayloadURL(base, pagePath string) string {
	base = strings.TrimRight(base, "/")
	pagePath = "/" + strings.Trim(pagePath, "/")
	if pagePath == "/" {
		return base + "/_payload.json"
	}
	return base + pagePath + "/_payload.json"
}

func resolveProviderReference(baseURL, reference string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	ref, err := url.Parse(strings.TrimSpace(reference))
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
}

func (d *Downloader) fetchYeguoPayload(ctx context.Context, pagePath string) (any, string, error) {
	var lastErr error
	for _, base := range d.providerBaseCandidates(ctx, sourceYeguo, yeguoBaseMirrors) {
		pageURL := strings.TrimRight(base, "/") + "/" + strings.TrimLeft(pagePath, "/")
		payloadURL := yeguoPayloadURL(base, pagePath)
		body, err := d.fetchProviderText(ctx, payloadURL, pageURL)
		if err == nil {
			if decoded := duanju51DecodePayload(body); decoded != nil {
				d.rememberProviderHost(sourceYeguo, base)
				return decoded, pageURL, nil
			}
			lastErr = fmt.Errorf("野果 payload 解码失败")
		} else {
			lastErr = err
		}

		// Nuxt 有时给 _payload.json 附带 buildId 查询串；直接路径失败时从页面发现真实地址。
		pageBody, pageErr := d.fetchProviderText(ctx, pageURL, strings.TrimRight(base, "/")+"/")
		if pageErr != nil {
			continue
		}
		for _, match := range yeguoPayloadRefPattern.FindAllStringSubmatch(pageBody, -1) {
			if len(match) < 2 {
				continue
			}
			candidate := resolveProviderReference(pageURL, match[1])
			if candidate == "" {
				continue
			}
			body, err = d.fetchProviderText(ctx, candidate, pageURL)
			if err != nil {
				lastErr = err
				continue
			}
			if decoded := duanju51DecodePayload(body); decoded != nil {
				d.rememberProviderHost(sourceYeguo, base)
				return decoded, pageURL, nil
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("野果页面暂未返回可解析数据")
	}
	return nil, "", lastErr
}

func walkYeguoMaps(value any, visit func(map[string]any)) {
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			visit(typed)
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		case map[any]any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
}

func yeguoText(row map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := row[key]; ok {
			text := strings.TrimSpace(nativeText(value))
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func yeguoBool(row map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		value, ok := row[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return typed, true
		case float64:
			return typed != 0, true
		case json.Number:
			return typed.String() != "0", true
		case string:
			switch strings.ToLower(strings.TrimSpace(typed)) {
			case "1", "true", "yes":
				return true, true
			case "0", "false", "no":
				return false, true
			}
		}
	}
	return false, false
}

func yeguoInt(row map[string]any, keys ...string) int {
	for _, key := range keys {
		if text := yeguoText(row, key); text != "" {
			if value, err := strconv.Atoi(text); err == nil {
				return value
			}
		}
	}
	return 0
}

func yeguoDramas(root any) []Drama {
	seen := map[string]bool{}
	var dramas []Drama
	walkYeguoMaps(root, func(row map[string]any) {
		id := yeguoText(row, "video_id", "videoId", "drama_id", "dramaId")
		title := yeguoText(row, "title", "video_title", "drama_name", "name")
		if id == "" || title == "" || seen[id] {
			return
		}
		seen[id] = true
		cover := yeguoText(row, "cover", "cover_url", "coverUrl", "cover_img")
		if strings.HasPrefix(cover, "//") {
			cover = "https:" + cover
		}
		// 站点的 content_access_type 分 free/coin/vip，但实测这三类都能从
		// /api/playlet/play 拿到 m3u8，播放接口并不拦付费。标成 VIP 只会让
		// 详情页弹"这是一集 VIP 内容"的确认框，所以列表一律按可播处理。
		vip := false
		dramas = append(dramas, Drama{
			VIP:          &vip,
			ID:           providerDramaID(sourceYeguo, id),
			Source:       sourceYeguo,
			SourceID:     id,
			Title:        title,
			Name:         title,
			Desc:         yeguoText(row, "intro", "description", "desc"),
			Intro:        yeguoText(row, "intro", "description", "desc"),
			Cover:        cover,
			CoverURL:     cover,
			EpisodeCount: yeguoText(row, "episode_count", "episodeCount", "latest_episode", "latestEpisode"),
			ChannelName:  "野果",
			CategoryName: yeguoText(row, "category_name", "categoryName", "tags"),
		})
	})
	return dramas
}

func yeguoFilterDramas(items []Drama, query string) []Drama {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return items
	}
	var result []Drama
	for _, drama := range items {
		text := strings.ToLower(drama.DisplayTitle() + " " + drama.Desc)
		if strings.Contains(text, query) {
			result = append(result, drama)
		}
	}
	return result
}

func (d *Downloader) fetchYeguoCatalogPage(ctx context.Context, page int, query string) ([]Drama, bool, error) {
	if page < 1 {
		page = 1
	}
	if query == "" && page == 1 {
		root, _, err := d.fetchYeguoPayload(ctx, "/")
		if err == nil {
			items := yeguoDramas(root)
			if len(items) > 0 {
				// 首页是多栏目聚合，不代表完整剧库；允许继续翻 API 历史分页。
				return items, true, nil
			}
		}
	}

	endpoint := "/api/theater/exploreList"
	attempts := []map[string]string{{
		"page": strconv.Itoa(page), "limit": strconv.Itoa(yeguoPageSize), "page_size": strconv.Itoa(yeguoPageSize),
	}}
	if strings.TrimSpace(query) != "" {
		endpoint = "/api/search/result"
		attempts = []map[string]string{
			{"keyword": query, "page": strconv.Itoa(page), "limit": strconv.Itoa(yeguoPageSize)},
			{"keywords": query, "page": strconv.Itoa(page), "limit": strconv.Itoa(yeguoPageSize)},
			{"search": query, "page": strconv.Itoa(page), "limit": strconv.Itoa(yeguoPageSize)},
		}
	}
	var lastErr error
	for _, params := range attempts {
		decoded, err := d.yeguoAPICall(ctx, endpoint, params)
		if err != nil {
			lastErr = err
			continue
		}
		items := yeguoDramas(decoded)
		if len(items) > 0 {
			return items, len(items) >= yeguoPageSize, nil
		}
	}
	if strings.TrimSpace(query) != "" && page == 1 {
		root, _, err := d.fetchYeguoPayload(ctx, "/")
		if err == nil {
			if items := yeguoFilterDramas(yeguoDramas(root), query); len(items) > 0 {
				return items, false, nil
			}
		}
	}
	// 搜索接口失败时如果静默返回空列表，UI 会显示"没有找到"，把站点故障
	// 伪装成"没有这部剧"。这里把真实错误抛上去。
	//
	// 但**翻页翻到空**是另一回事（0.8.24）：目录只有 52 页，第 53 页起
	// 服务端回空列表，那是正常收尾。以前这里也抛错，catalogdump 于是把
	// 「野果 1033 条」整站判成失败 → 快照只能沿用上一轮，永远停在旧数据。
	// 分清"搜索没结果"（要报错）与"翻页到底"（要正常收尾）。
	if strings.TrimSpace(query) == "" {
		return nil, false, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("野果暂未返回剧集")
	}
	return nil, false, lastErr
}

func yeguoEpisodeMarker(episodeID string) string {
	return "yeguo-play://episode/" + url.PathEscape(strings.TrimSpace(episodeID))
}

func yeguoMarkerEpisode(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "yeguo-play" || parsed.Host != "episode" {
		return ""
	}
	value, _ := url.PathUnescape(strings.Trim(parsed.Path, "/"))
	return strings.TrimSpace(value)
}

// yeguoDramaObject 从详情响应里找出"剧级对象"：带 episodes 数组，且 id 与请求一致。
// 站点的 related_list 里挂着别的剧（同样带 episodes 数组），泛走整棵树会把别人的
// 分集算进来，所以必须锚定在剧级对象上。
func yeguoDramaObject(root any, sourceID string) map[string]any {
	var target map[string]any
	var fallback map[string]any
	walkYeguoMaps(root, func(row map[string]any) {
		if target != nil {
			return
		}
		episodes, ok := row["episodes"].([]any)
		if !ok || len(episodes) == 0 {
			return
		}
		if fallback == nil {
			fallback = row
		}
		id := yeguoText(row, "video_id", "videoId", "playlet_id", "playletId", "drama_id", "dramaId", "id")
		if sourceID != "" && id == sourceID {
			target = row
		}
	})
	if target != nil {
		return target
	}
	return fallback
}

type yeguoEpisode struct {
	id    string
	index int
	title string
}

// yeguoEpisodeRows 读剧级对象的 episodes[]。真实分集字段是 id（不是 episode_id），
// 序号字段是 sort；两者都缺时退回数组下标。
func yeguoEpisodeRows(drama map[string]any) []yeguoEpisode {
	raw, _ := drama["episodes"].([]any)
	seen := map[string]bool{}
	rows := make([]yeguoEpisode, 0, len(raw))
	for position, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if adv, exists := yeguoBool(row, "is_adv", "isAdv"); exists && adv {
			continue
		}
		id := yeguoText(row, "id", "episode_id", "episodeId")
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		index := yeguoInt(row, "sort", "episode_number", "episodeNumber", "episode_no", "episodeNo", "episode", "index")
		if index <= 0 {
			index = position + 1
		}
		rows = append(rows, yeguoEpisode{
			id:    id,
			index: index,
			title: yeguoText(row, "title", "episode_title", "episodeTitle", "name"),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].index < rows[j].index })
	return rows
}

func (d *Downloader) fetchYeguoDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return Drama{}, nil, fmt.Errorf("野果剧集 ID 为空")
	}
	path := yeguoDetailPath(sourceID)
	root, pageURL, err := d.fetchYeguoPayload(ctx, path)
	if err != nil {
		return Drama{}, nil, err
	}
	// 0.8.9：分集只从剧级对象的 episodes[] 取。原来泛走整棵树、用 episode_id 作键，
	// 但站点真实字段是 id，episode_id 恒为空 —— 结果抓到了 related_list 里别的剧的
	// 分集（标题串剧），播放接口回"剧集不存在或不属于该短剧"。
	meta := yeguoDramaObject(root, sourceID)
	if meta == nil {
		return Drama{}, nil, fmt.Errorf("野果详情未返回剧集，站点结构可能已变化")
	}
	episodes := yeguoEpisodeRows(meta)
	if len(episodes) == 0 {
		return Drama{}, nil, fmt.Errorf("野果详情未返回分集，站点结构可能已变化")
	}

	// 不再逐集探测播放地址：原先"每集真播一遍"才放行，32 部首页要 110 秒。
	// 实测 content_access_type 为 free/coin/vip 的剧集都能从 /api/playlet/play
	// 拿到 m3u8（站点没有在播放接口上拦付费），所以既不标 VIP 也不做全集校验。
	chapters := make([]Chapter, 0, len(episodes))
	for _, episode := range episodes {
		title := strings.TrimSpace(episode.title)
		if title == "" {
			title = fmt.Sprintf("第%d集", episode.index)
		}
		chapter := Chapter{
			ID:             providerChapterID(sourceYeguo, sourceID, episode.id),
			Source:         sourceYeguo,
			Title:          title,
			PageURL:        pageURL,
			Referer:        pageURL,
			CurrentEpisode: rawEpisode(episode.index),
		}
		chapter.VideoURL = yeguoEpisodeMarker(episode.id)
		chapters = append(chapters, chapter)
	}

	title := firstNonEmpty(yeguoText(meta, "title", "video_title", "drama_name", "name"), "野果 "+sourceID)
	cover := yeguoText(meta, "cover", "cover_url", "coverUrl", "cover_img")
	if strings.HasPrefix(cover, "//") {
		cover = "https:" + cover
	}
	description := yeguoText(meta, "intro", "description", "desc")
	vip := false
	drama := Drama{
		VIP:          &vip,
		ID:           providerDramaID(sourceYeguo, sourceID),
		Source:       sourceYeguo,
		SourceID:     sourceID,
		Title:        title,
		Name:         title,
		Desc:         description,
		Intro:        description,
		Cover:        cover,
		CoverURL:     cover,
		EpisodeCount: strconv.Itoa(len(chapters)),
		TotalEpisode: strconv.Itoa(len(chapters)),
		ChannelName:  "野果",
	}
	return drama, chapters, nil
}

func randomHex32() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func (d *Downloader) yeguoAPICall(ctx context.Context, endpoint string, extras map[string]string) (any, error) {
	oauthID, err := randomHex32()
	if err != nil {
		return nil, err
	}
	traceID, err := randomHex32()
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"bundleId":   {"com.pwa.mater"},
		"version":    {"1.3.2"},
		"oauth_type": {"web"},
		"language":   {"zh"},
		"via":        {"pwa"},
		"oauth_id":   {oauthID},
		"trace_id":   {traceID},
		"token":      {""},
	}
	for key, value := range extras {
		form.Set(key, value)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(yeguoAPIBaseURL, "/")+endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", d.providerBaseURL(sourceYeguo))
	req.Header.Set("Referer", d.providerBaseURL(sourceYeguo)+"/")
	req.Header.Set("User-Agent", userAgent)
	resp, err := d.doCatalogRequestWithTimeout(req, 20*time.Second)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	// 0.8.10：**读完立刻关掉 body**，把源级并发门还给限流器。
	//
	// 下面的 `decryptYeguoData` 在密钥还没缓存时会触发密钥发现，而发现要再发
	// HTTP 请求；限流器对同一个站源只允许 1 个并发请求，如果这里还占着门，
	// 发现就在等自己 —— 一路等到发现超时，用户看到的是「野果整站不可用」。
	// 实测：不预热时点播 27 秒后失败，预热后 1.8 秒拿到 m3u8。
	resp.Body.Close()
	if len(body) > 4<<20 {
		return nil, fmt.Errorf("野果 API 响应过大")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("野果 API HTTP %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var envelope yeguoAPIEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("野果 API 响应格式异常: %w", err)
	}
	if code := strings.TrimSpace(nativeText(envelope.Errcode)); code != "" && code != "0" {
		return nil, fmt.Errorf("野果 API 返回 %s: %s", code, firstNonEmpty(nativeText(envelope.Message), nativeText(envelope.Msg), "请求失败"))
	}
	ciphertext, encrypted := envelope.Data.(string)
	if !encrypted {
		return envelope.Data, nil
	}
	decoded, err := d.decryptYeguoData(ctx, ciphertext, false)
	if err != nil {
		// 前端刚更新时缓存 key 可能已经过期，强制重扫一次 chunk。
		// 但如果这次连 key 都没发现出来，重扫只会把等待时间翻倍。
		if d.yeguoKeyCached() {
			return d.decryptYeguoData(ctx, ciphertext, true)
		}
		return nil, err
	}
	return decoded, nil
}

func (d *Downloader) yeguoKeyCached() bool {
	d.yeguoCryptoMu.Lock()
	defer d.yeguoCryptoMu.Unlock()
	return d.yeguoCrypto.Key != "" && time.Now().Before(d.yeguoCryptoExpires)
}

func (d *Downloader) decryptYeguoData(ctx context.Context, ciphertext string, force bool) (any, error) {
	config, err := d.yeguoCryptoConfig(ctx, force)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ciphertext))
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(ciphertext))
	}
	if err != nil {
		return nil, fmt.Errorf("野果密文 base64 无效: %w", err)
	}
	plain, err := decryptYeguoCBC(raw, []byte(config.Key), []byte(config.IV))
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("野果解密 JSON 无效: %w", err)
	}
	return decoded, nil
}

func decryptYeguoCBC(raw, key, iv []byte) ([]byte, error) {
	if len(key) != 16 || len(iv) != aes.BlockSize || len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("野果 AES 参数无效")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(raw))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, raw)
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > aes.BlockSize || padding > len(plain) {
		return nil, fmt.Errorf("野果 AES 填充无效")
	}
	for _, value := range plain[len(plain)-padding:] {
		if int(value) != padding {
			return nil, fmt.Errorf("野果 AES 填充无效")
		}
	}
	return plain[:len(plain)-padding], nil
}

func (d *Downloader) yeguoCryptoConfig(ctx context.Context, force bool) (yeguoCryptoConfig, error) {
	d.yeguoCryptoMu.Lock()
	defer d.yeguoCryptoMu.Unlock()
	if !force && d.yeguoCrypto.Key != "" && time.Now().Before(d.yeguoCryptoExpires) {
		return d.yeguoCrypto, nil
	}
	// 站点被拦截时整个发现流程会一直重试到调用方的 deadline（实测能吃掉
	// 4 分钟），期间每次请求都白等。这里给发现本身加一个上限，并在失败后
	// 短暂拒绝重试，让用户尽快看到"站点当前不可用"。
	if !force && time.Now().Before(d.yeguoCryptoRetryAfter) {
		return yeguoCryptoConfig{}, fmt.Errorf("野果站点当前不可用，请稍后重试")
	}
	discoverCtx, cancel := context.WithTimeout(ctx, yeguoCryptoDiscoverTimeout)
	defer cancel()
	config, err := d.discoverYeguoCrypto(discoverCtx)
	if err != nil {
		d.yeguoCryptoRetryAfter = time.Now().Add(yeguoCryptoRetryBackoff)
		return yeguoCryptoConfig{}, err
	}
	d.yeguoCrypto = config
	d.yeguoCryptoExpires = time.Now().Add(yeguoCryptoTTL)
	return config, nil
}

func (d *Downloader) discoverYeguoCrypto(ctx context.Context) (yeguoCryptoConfig, error) {
	// 密钥发现有可能在"某个野果请求还占着源级并发门"时被触发
	// （例如 `yeguoAPICall` 拿到密文后立刻解密）。这里整体跳过源级门，
	// 否则发现请求会排队等自己，直接把发现拖到超时。
	// 并发量本身是可控的：1 个首页 + 每批最多 8 个 chunk。
	ctx = context.WithValue(ctx, skipCatalogGateKey{}, true)
	var lastErr error
	for _, base := range d.providerBaseCandidates(ctx, sourceYeguo, yeguoBaseMirrors) {
		home := strings.TrimRight(base, "/") + "/"
		// 0.8.10：首页也改直取。
		//
		// 原来这里走 `fetchProviderText`，也就是镜像竞速（候选 × 2 副本 × 3 波）。
		// 只要其中一个镜像在重置连接，竞速会一路等到三波超时，单个候选就能吃掉
		// 20 秒以上；两个候选叠起来正好撞上 40 秒的发现超时，表现就是
		// 「野果整站不可用」——而实测密钥其实一直都在（只是没时间扫到）。
		// 现在按候选顺序直取，失败就换下一个，最坏也就 8 秒 × 候选数。
		body, err := d.fetchProviderTextTimeout(ctx, home, home, home, 10*time.Second)
		if err != nil {
			lastErr = err
			continue
		}
		queue := make([]string, 0, 96)
		for _, match := range yeguoScriptSrcPattern.FindAllStringSubmatch(body, -1) {
			if len(match) > 1 {
				if script := resolveProviderReference(home, match[1]); script != "" {
					queue = append(queue, script)
				}
			}
		}
		// 补上 importmap / modulepreload 里的 chunk 引用（0.8.9）。
		for _, match := range yeguoNuxtRefPattern.FindAllStringSubmatch(body, -1) {
			if len(match) > 1 {
				if chunk := resolveProviderReference(home, match[1]); chunk != "" {
					queue = append(queue, chunk)
				}
			}
		}
		if len(queue) == 0 {
			// 正常的 Nuxt 首页至少有 20 多个 _nuxt chunk 引用。一个都没有，
			// 说明拿到的是兜底页而不是真首页，此时报"配置已变化"会误导排查。
			lastErr = fmt.Errorf("野果首页没有前端脚本引用，可能被站点拦截")
			continue
		}
		seen := map[string]bool{}
		for len(queue) > 0 && len(seen) < yeguoMaxChunkScan {
			batch := make([]string, 0, 8)
			for len(queue) > 0 && len(batch) < 8 && len(seen) < yeguoMaxChunkScan {
				current := queue[0]
				queue = queue[1:]
				if seen[current] {
					continue
				}
				parsed, parseErr := url.Parse(current)
				homeURL, homeErr := url.Parse(home)
				if parseErr != nil || homeErr != nil || !strings.EqualFold(parsed.Hostname(), homeURL.Hostname()) {
					continue
				}
				seen[current] = true
				batch = append(batch, current)
			}
			if len(batch) == 0 {
				continue
			}
			type result struct {
				url  string
				body string
				err  error
			}
			results := make(chan result, len(batch))
			for _, address := range batch {
				go func(address string) {
					// chunk 和首页在同一个域名上，必须直取。走 fetchProviderText 会
					// 触发镜像竞速（4 个候选 × 2 副本 × 3 波），一个 chunk 最多
					// 24 次请求；30 个 chunk 叠加起来，站点一被拦就要跑几分钟。
					//
					// 0.8.10：再补上「跳过源级串行门」。
					// 限流器对同一个站源只允许 **1 个并发请求**（`gate.busy`），
					// 每个请求之间还隔 250ms，于是 30 个 chunk 要排 15 秒以上，
					// 经常把密钥发现拖过超时。这里和镜像竞速一样带上
					// `skipCatalogGateKey`：批量大小已经限死在 8，不会打爆站点。
					chunkCtx := context.WithValue(ctx, skipCatalogGateKey{}, true)
					chunk, fetchErr := d.fetchProviderTextTimeout(chunkCtx, address, address, home, 6*time.Second)
					results <- result{url: address, body: chunk, err: fetchErr}
				}(address)
			}
			for range batch {
				item := <-results
				if item.err != nil {
					lastErr = item.err
					continue
				}
				if strings.Contains(item.body, "sign_key") || strings.Contains(strings.ToLower(item.body), "pkcs7") || strings.Contains(strings.ToUpper(item.body), "CBC") {
					key := ""
					if match := yeguoKeyWrappedPattern.FindStringSubmatch(item.body); len(match) > 1 {
						key = match[1]
					} else if match := yeguoKeyPattern.FindStringSubmatch(item.body); len(match) > 1 {
						key = match[1]
					}
					iv := ""
					if match := yeguoIVWrappedPattern.FindStringSubmatch(item.body); len(match) > 1 {
						iv = match[1]
					} else if match := yeguoIVPattern.FindStringSubmatch(item.body); len(match) > 1 {
						iv = match[1]
					}
					if len(key) == 16 && len(iv) == 16 {
						return yeguoCryptoConfig{Key: key, IV: iv}, nil
					}
				}
				for _, match := range yeguoJSRefPattern.FindAllStringSubmatch(item.body, -1) {
					if len(match) < 2 {
						continue
					}
					if next := resolveProviderReference(item.url, match[1]); next != "" && !seen[next] {
						queue = append(queue, next)
					}
				}
			}
		}
	}
	if lastErr != nil {
		return yeguoCryptoConfig{}, fmt.Errorf("野果前端加密配置发现失败: %w", lastErr)
	}
	return yeguoCryptoConfig{}, fmt.Errorf("野果前端加密配置已变化，请更新站源适配")
}

func yeguoPlaybackMap(value any) map[string]any {
	if row, ok := value.(map[string]any); ok {
		if yeguoText(row, "video_url", "video_url_h265", "play_url", "manifest_url", "url") != "" {
			return row
		}
		for _, child := range row {
			if found := yeguoPlaybackMap(child); found != nil {
				return found
			}
		}
	}
	if list, ok := value.([]any); ok {
		for _, child := range list {
			if found := yeguoPlaybackMap(child); found != nil {
				return found
			}
		}
	}
	return nil
}

func (d *Downloader) yeguoFullPlaybackURL(ctx context.Context, dramaID, episodeID string) (string, error) {
	attempts := []map[string]string{
		{"playlet_id": dramaID, "video_id": dramaID, "episode_id": episodeID},
		{"video_id": dramaID, "episode_id": episodeID},
		{"episode_id": episodeID},
	}
	var lastErr error
	for _, params := range attempts {
		decoded, err := d.yeguoAPICall(ctx, "/api/playlet/play", params)
		if err != nil {
			lastErr = err
			continue
		}
		row := yeguoPlaybackMap(decoded)
		if row == nil {
			lastErr = fmt.Errorf("野果正片接口未返回播放对象")
			continue
		}
		if returnedDrama := yeguoText(row, "playlet_id", "video_id"); returnedDrama != "" && returnedDrama != dramaID {
			lastErr = fmt.Errorf("野果返回剧集与请求不一致")
			continue
		}
		if returnedEpisode := yeguoText(row, "id", "episode_id", "episodeId"); returnedEpisode != "" && returnedEpisode != episodeID {
			lastErr = fmt.Errorf("野果返回分集与请求不一致")
			continue
		}
		if adv, exists := yeguoBool(row, "is_adv", "isAdv"); exists && adv {
			lastErr = fmt.Errorf("野果返回的是广告而非正片")
			continue
		}
		mediaURL := yeguoText(row, "video_url", "video_url_h265", "play_url", "manifest_url", "url")
		if !strings.HasPrefix(mediaURL, "http://") && !strings.HasPrefix(mediaURL, "https://") {
			lastErr = fmt.Errorf("野果正片接口未返回有效播放地址")
			continue
		}
		return mediaURL, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("野果正片播放地址获取失败")
	}
	return "", lastErr
}

func yeguoFindMediaURL(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"manifest_url", "manifestUrl", "play_url", "playUrl", "video_url", "videoUrl", "hls", "m3u8", "url"} {
			if candidate := yeguoFindMediaURL(typed[key]); candidate != "" {
				return candidate
			}
		}
		for _, child := range typed {
			if candidate := yeguoFindMediaURL(child); candidate != "" {
				return candidate
			}
		}
	case []any:
		for _, child := range typed {
			if candidate := yeguoFindMediaURL(child); candidate != "" {
				return candidate
			}
		}
	case string:
		candidate := strings.TrimSpace(typed)
		lower := strings.ToLower(candidate)
		if strings.HasPrefix(candidate, "http://") || strings.HasPrefix(candidate, "https://") {
			if strings.Contains(lower, ".m3u8") || strings.Contains(lower, ".mp4") || strings.Contains(lower, "manifest") {
				return candidate
			}
		}
	}
	return ""
}

func (d *Downloader) resolveYeguoPlayback(ctx context.Context, task Task) (providerMedia, error) {
	episodeID := yeguoMarkerEpisode(task.Chapter.VideoURL)
	if episodeID == "" {
		return providerMedia{}, fmt.Errorf("野果分集播放标识无效，请刷新详情")
	}
	_, dramaID, valid := splitProviderDramaID(task.DramaID)
	if !valid || dramaID == "" {
		return providerMedia{}, fmt.Errorf("野果剧集信息无效")
	}
	mediaURL, err := d.yeguoFullPlaybackURL(ctx, dramaID, episodeID)
	if err != nil {
		return providerMedia{}, err
	}
	return providerMedia{URL: mediaURL, Referer: firstNonEmpty(task.Chapter.PageURL, d.providerBaseURL(sourceYeguo)+"/")}, nil
}
