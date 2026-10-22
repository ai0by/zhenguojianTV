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
	"sync"
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
)

var yeguoBaseMirrors = []string{
	"https://blue.ekzhadks.cc",
	"https://yeguodj.com",
	"https://ygdj16.com",
	"https://hoamqvgal.cc",
}

var (
	yeguoPayloadRefPattern = regexp.MustCompile(`(?i)([^"'<>\s]*_payload\.json(?:\?[^"'<>\s]*)?)`)
	yeguoScriptSrcPattern  = regexp.MustCompile(`(?is)<script\b[^>]*\bsrc=["']([^"']+\.js(?:\?[^"']*)?)["'][^>]*>`)
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

func yeguoHasExplicitAccess(row map[string]any) bool {
	for _, key := range []string{"can_play", "canPlay", "playable", "owned", "is_free", "isFree", "free", "content_access_type", "contentAccessType", "charge_type", "chargeType", "access_type"} {
		if _, ok := row[key]; ok {
			return true
		}
	}
	return false
}

func yeguoLocked(row map[string]any) bool {
	if playable, ok := yeguoBool(row, "can_play", "canPlay", "playable", "owned"); ok {
		return !playable
	}
	if free, ok := yeguoBool(row, "is_free", "isFree", "free"); ok && free {
		return false
	}
	access := strings.ToLower(yeguoText(row, "content_access_type", "contentAccessType", "charge_type", "chargeType", "access_type"))
	switch access {
	case "coin", "vip", "paid", "pay", "member", "membership":
		return true
	case "free", "public", "":
		return false
	default:
		// 未知权益类型不主动当作可绕过的免费内容。
		return true
	}
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
		locked := yeguoLocked(row)
		vip := locked
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
				return d.filterYeguoFullyPlayable(ctx, items), true, nil
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
			return d.filterYeguoFullyPlayable(ctx, items), len(items) >= yeguoPageSize, nil
		}
	}
	if strings.TrimSpace(query) != "" && page == 1 {
		root, _, err := d.fetchYeguoPayload(ctx, "/")
		if err == nil {
			items := yeguoFilterDramas(yeguoDramas(root), query)
			return d.filterYeguoFullyPlayable(ctx, items), false, nil
		}
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

func (d *Downloader) fetchYeguoDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return Drama{}, nil, fmt.Errorf("野果剧集 ID 为空")
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, 15*time.Second)
	fullyPlayable := d.yeguoDramaFullyPlayable(probeCtx, sourceID)
	probeCancel()
	if !fullyPlayable {
		return Drama{}, nil, fmt.Errorf("野果该剧未通过全集正片校验，已隐藏")
	}
	path := yeguoDetailPath(sourceID)
	root, pageURL, err := d.fetchYeguoPayload(ctx, path)
	if err != nil {
		return Drama{}, nil, err
	}

	var meta map[string]any
	type episodeRow struct {
		id             string
		index          int
		title          string
		locked         bool
		explicitAccess bool
	}
	seenEpisodes := map[string]bool{}
	var episodes []episodeRow
	walkYeguoMaps(root, func(row map[string]any) {
		if meta == nil && yeguoText(row, "video_id", "videoId", "drama_id", "dramaId") == sourceID && yeguoText(row, "title", "video_title", "drama_name", "name") != "" {
			meta = row
		}
		episodeID := yeguoText(row, "episode_id", "episodeId")
		if episodeID == "" || seenEpisodes[episodeID] {
			return
		}
		seenEpisodes[episodeID] = true
		index := yeguoInt(row, "episode_number", "episodeNumber", "episode_no", "episodeNo", "episode", "sort", "index")
		episodes = append(episodes, episodeRow{
			id:             episodeID,
			index:          index,
			title:          yeguoText(row, "title", "episode_title", "episodeTitle", "name"),
			locked:         false,
			explicitAccess: yeguoHasExplicitAccess(row),
		})
	})
	if len(episodes) == 0 {
		return Drama{}, nil, fmt.Errorf("野果详情未返回分集，站点结构可能已变化")
	}
	metaLocked := false
	for index := range episodes {
		// 当前站点的剧级对象带 content_access_type，但分集对象不一定重复携带。
		// 若剧级已经明确 coin/vip，而某集没有独立权益字段，按锁定处理；
		// 只有分集自己明确 can_play/free/owned 时才允许覆盖剧级状态。
		if metaLocked && !episodes[index].explicitAccess {
			episodes[index].locked = true
		}
		if episodes[index].index <= 0 {
			episodes[index].index = index + 1
		}
	}
	sort.SliceStable(episodes, func(i, j int) bool { return episodes[i].index < episodes[j].index })

	chapters := make([]Chapter, 0, len(episodes))
	freeFound := false
	for _, episode := range episodes {
		title := strings.TrimSpace(episode.title)
		if title == "" {
			title = fmt.Sprintf("第%d集", episode.index)
		}
		chapter := Chapter{
			VIP:            episode.locked,
			ID:             providerChapterID(sourceYeguo, sourceID, episode.id),
			Source:         sourceYeguo,
			Title:          title,
			PageURL:        pageURL,
			Referer:        pageURL,
			CurrentEpisode: rawEpisode(episode.index),
		}
		chapter.VideoURL = yeguoEpisodeMarker(episode.id)
		if !episode.locked {
			freeFound = true
		}
		chapters = append(chapters, chapter)
	}

	title := "野果 " + sourceID
	cover, description := "", ""
	if meta != nil {
		title = firstNonEmpty(yeguoText(meta, "title", "video_title", "drama_name", "name"), title)
		cover = yeguoText(meta, "cover", "cover_url", "coverUrl", "cover_img")
		description = yeguoText(meta, "intro", "description", "desc")
	}
	if strings.HasPrefix(cover, "//") {
		cover = "https:" + cover
	}
	vip := !freeFound
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
		return d.decryptYeguoData(ctx, ciphertext, true)
	}
	return decoded, nil
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
	config, err := d.discoverYeguoCrypto(ctx)
	if err != nil {
		return yeguoCryptoConfig{}, err
	}
	d.yeguoCrypto = config
	d.yeguoCryptoExpires = time.Now().Add(yeguoCryptoTTL)
	return config, nil
}

func (d *Downloader) discoverYeguoCrypto(ctx context.Context) (yeguoCryptoConfig, error) {
	var lastErr error
	for _, base := range d.providerBaseCandidates(ctx, sourceYeguo, yeguoBaseMirrors) {
		home := strings.TrimRight(base, "/") + "/"
		body, err := d.fetchProviderText(ctx, home, home)
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
					chunk, fetchErr := d.fetchProviderText(ctx, address, home)
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

type yeguoPlayableCacheEntry struct {
	playable bool
	expires  time.Time
}

var yeguoPlayableCache sync.Map

func yeguoEpisodeIDs(value any, sourceID string) []string {
	sourceID = strings.TrimSpace(sourceID)
	var fallback []any
	var target []any
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if episodes, ok := typed["episodes"].([]any); ok && len(episodes) > 0 {
				if fallback == nil {
					fallback = episodes
				}
				id := yeguoText(typed, "video_id", "videoId", "playlet_id", "playletId", "drama_id", "dramaId", "id")
				if sourceID != "" && id == sourceID {
					target = episodes
					return
				}
			}
			for _, child := range typed {
				if target == nil {
					walk(child)
				}
			}
		case []any:
			for _, child := range typed {
				if target == nil {
					walk(child)
				}
			}
		}
	}
	walk(value)
	rows := target
	if rows == nil {
		rows = fallback
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(rows))
	for _, item := range rows {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if adv, exists := yeguoBool(row, "is_adv", "isAdv"); exists && adv {
			continue
		}
		id := yeguoText(row, "id", "episode_id", "episodeId")
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
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

func (d *Downloader) yeguoDramaFullyPlayable(ctx context.Context, sourceID string) bool {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return false
	}
	if cached, ok := yeguoPlayableCache.Load(sourceID); ok {
		entry := cached.(yeguoPlayableCacheEntry)
		if time.Now().Before(entry.expires) {
			return entry.playable
		}
		yeguoPlayableCache.Delete(sourceID)
	}
	detail, err := d.yeguoAPICall(ctx, "/api/playlet/detail", map[string]string{
		"video_id":      sourceID,
		"id":            sourceID,
		"episode_id":    "0",
		"related_limit": "0",
	})
	if err != nil {
		return false
	}
	episodeIDs := yeguoEpisodeIDs(detail, sourceID)
	if len(episodeIDs) == 0 {
		return false
	}
	playable := true
	for _, episodeID := range episodeIDs {
		if _, err := d.yeguoFullPlaybackURL(ctx, sourceID, episodeID); err != nil {
			playable = false
			break
		}
	}
	ttl := 30 * time.Minute
	if !playable {
		ttl = 2 * time.Minute
	}
	yeguoPlayableCache.Store(sourceID, yeguoPlayableCacheEntry{playable: playable, expires: time.Now().Add(ttl)})
	return playable
}

func (d *Downloader) filterYeguoFullyPlayable(ctx context.Context, items []Drama) []Drama {
	if len(items) == 0 {
		return items
	}
	allowed := make([]bool, len(items))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i := range items {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			allowed[index] = d.yeguoDramaFullyPlayable(probeCtx, items[index].SourceID)
		}(i)
	}
	wg.Wait()
	filtered := make([]Drama, 0, len(items))
	for i, drama := range items {
		if allowed[i] {
			vip := false
			drama.VIP = &vip
			filtered = append(filtered, drama)
		}
	}
	return filtered
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
