package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 香蕉短剧是明文 REST 站点。播放正片必须先创建 guest session，
// 再调用 /api/playback/sessions。这里明确不调用 trial/sessions：
// 如果服务端返回 PAYMENT_REQUIRED，就把该集作为受限内容交给 UI，
// 不把试看地址伪装成“完整可播放”。
const (
	sourceXiangjiao     = "xiangjiao"
	xiangjiaoBaseURL    = "https://xiangjiaoai.ai"
	xiangjiaoSessionTTL = 40 * time.Minute
)

var xiangjiaoBaseMirrors = []string{
	"https://xiangjiaoai.ai",
	"https://xiangjiao01.com",
}

type xiangjiaoHTTPError struct {
	Status  int
	Code    string
	Message string
}

func (e *xiangjiaoHTTPError) Error() string {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "请求失败"
	}
	if e.Code != "" {
		return fmt.Sprintf("香蕉接口 %s: %s", e.Code, message)
	}
	return fmt.Sprintf("香蕉接口 HTTP %d: %s", e.Status, message)
}

func xiangjiaoMirrorHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, origin := range xiangjiaoBaseMirrors {
		parsed, err := url.Parse(origin)
		if err == nil && strings.EqualFold(parsed.Hostname(), host) {
			return true
		}
	}
	return false
}

func xiangjiaoText(row map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := row[key]; ok {
			if text := strings.TrimSpace(nativeText(value)); text != "" {
				return text
			}
		}
	}
	return ""
}

func xiangjiaoBool(row map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		value, ok := row[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return typed, true
		case json.Number:
			return typed.String() != "0", true
		case float64:
			return typed != 0, true
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

func xiangjiaoInt(row map[string]any, keys ...string) int {
	for _, key := range keys {
		if text := xiangjiaoText(row, key); text != "" {
			if value, err := strconv.Atoi(text); err == nil {
				return value
			}
		}
	}
	return 0
}

func xiangjiaoMap(row map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := row[key].(map[string]any); ok {
			return value
		}
	}
	return nil
}

func walkXiangjiaoMaps(value any, visit func(map[string]any)) {
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
		}
	}
	walk(value)
}

func xiangjiaoPaymentRequired(apiErr *xiangjiaoHTTPError) bool {
	if apiErr == nil {
		return false
	}
	code := strings.ToLower(strings.TrimSpace(apiErr.Code))
	message := strings.ToLower(strings.TrimSpace(apiErr.Message))
	for _, marker := range []string{"payment_required", "payment required", "paywall", "purchase", "membership", "need_pay", "need pay", "vip"} {
		if strings.Contains(code, marker) || strings.Contains(message, marker) {
			return true
		}
	}
	for _, marker := range []string{"会员", "购买", "付费", "充值", "需要支付"} {
		if strings.Contains(apiErr.Message, marker) {
			return true
		}
	}
	return false
}

func (d *Downloader) xiangjiaoJSON(ctx context.Context, method, path string, payload any, bearer string) (any, error) {
	var lastErr error
	for _, base := range d.providerBaseCandidates(ctx, sourceXiangjiao, xiangjiaoBaseMirrors) {
		var body io.Reader
		if payload != nil {
			encoded, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			body = bytes.NewReader(encoded)
		}
		address := strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
		req, err := http.NewRequestWithContext(ctx, method, address, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Origin", strings.TrimRight(base, "/"))
		req.Header.Set("Referer", strings.TrimRight(base, "/")+"/")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if strings.TrimSpace(bearer) != "" {
			req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(bearer))
		}
		resp, err := d.doCatalogRequestWithTimeout(req, 20*time.Second)
		if err != nil {
			lastErr = err
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if len(data) > 4<<20 {
			return nil, fmt.Errorf("香蕉接口响应过大")
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var decoded any
		decodeErr := decoder.Decode(&decoded)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			apiErr := &xiangjiaoHTTPError{Status: resp.StatusCode}
			if root, ok := decoded.(map[string]any); ok {
				apiErr.Code = firstNonEmpty(xiangjiaoText(root, "code", "error_code", "errorCode"), xiangjiaoText(xiangjiaoMap(root, "error"), "code"))
				apiErr.Message = firstNonEmpty(xiangjiaoText(root, "message", "msg", "detail"), xiangjiaoText(xiangjiaoMap(root, "error"), "message", "msg", "detail"))
			}
			lastErr = apiErr
			normalizedPath := strings.TrimRight(path, "/")
			// 播放接口的 401 交给上层刷新游客 token；明确的会员/购买拒绝
			// 也不能通过切换镜像重试。其他 401/403 可能是单个域名的 CDN/WAF
			// 限制，继续尝试备用镜像，避免把镜像故障误判成“没有内容”。
			if normalizedPath == "/api/playback/sessions" {
				if resp.StatusCode == http.StatusUnauthorized {
					return nil, apiErr
				}
				if resp.StatusCode == http.StatusForbidden && xiangjiaoPaymentRequired(apiErr) {
					return nil, apiErr
				}
			}
			continue
		}
		if decodeErr != nil {
			lastErr = fmt.Errorf("香蕉接口 JSON 无效: %w", decodeErr)
			continue
		}
		d.rememberProviderHost(sourceXiangjiao, base)
		return decoded, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("香蕉接口请求失败")
	}
	return nil, lastErr
}

func xiangjiaoPayloadData(value any) any {
	if root, ok := value.(map[string]any); ok {
		if data, exists := root["data"]; exists {
			return data
		}
	}
	return value
}

func xiangjiaoDramaFromMap(row map[string]any) Drama {
	id := firstNonEmpty(
		xiangjiaoText(row, "dramaId", "drama_id", "shortDramaId", "short_drama_id", "playletId", "playlet_id", "videoId", "video_id"),
		xiangjiaoText(row, "id"),
	)
	title := xiangjiaoText(row, "title", "name", "dramaTitle", "drama_title", "videoTitle", "video_title")
	if id == "" || title == "" {
		return Drama{}
	}
	cover := xiangjiaoText(row, "coverUrl", "cover_url", "cover", "poster")
	charge := strings.ToLower(xiangjiaoText(row, "chargeType", "charge_type"))
	var vip *bool
	if charge != "" {
		value := charge != "free" && charge != "public"
		vip = &value
	}
	return Drama{
		VIP:          vip,
		ID:           providerDramaID(sourceXiangjiao, id),
		Source:       sourceXiangjiao,
		SourceID:     id,
		Title:        title,
		Name:         title,
		Desc:         xiangjiaoText(row, "description", "tagline", "intro"),
		Intro:        xiangjiaoText(row, "description", "tagline", "intro"),
		Cover:        cover,
		CoverURL:     cover,
		EpisodeCount: firstNonEmpty(xiangjiaoText(row, "episodeCount", "episode_count"), xiangjiaoText(row, "latestEpisode", "latest_episode")),
		CategoryName: xiangjiaoText(row, "categoryName", "category_name", "kind"),
		ChannelName:  "香蕉",
		Remark:       xiangjiaoText(row, "serialStatus", "serial_status", "badge"),
	}
}

func xiangjiaoDramas(value any) []Drama {
	seen := map[string]bool{}
	var items []Drama
	walkXiangjiaoMaps(xiangjiaoPayloadData(value), func(row map[string]any) {
		drama := xiangjiaoDramaFromMap(row)
		if drama.ID == "" || seen[drama.ID] {
			return
		}
		seen[drama.ID] = true
		items = append(items, drama)
	})
	return items
}

func (d *Downloader) fetchXiangjiaoCatalogPage(ctx context.Context, page int, query string) ([]Drama, bool, error) {
	if page > 1 {
		return nil, false, nil
	}
	decoded, err := d.xiangjiaoJSON(ctx, http.MethodGet, "/api/home/hot", nil, "")
	if err != nil {
		return nil, false, err
	}
	items := xiangjiaoDramas(decoded)
	query = strings.ToLower(strings.TrimSpace(query))
	if query != "" {
		filtered := items[:0]
		for _, drama := range items {
			if strings.Contains(strings.ToLower(drama.DisplayTitle()+" "+drama.Desc), query) {
				filtered = append(filtered, drama)
			}
		}
		items = filtered
	}
	return d.filterXiangjiaoFullyPlayable(ctx, items), false, nil
}

func xiangjiaoPaymentRequiredFromError(err error) bool {
	if apiErr, ok := err.(*xiangjiaoHTTPError); ok {
		return xiangjiaoPaymentRequired(apiErr)
	}
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "会员") ||
		strings.Contains(message, "购买") ||
		strings.Contains(message, "付费") ||
		strings.Contains(message, "payment") ||
		strings.Contains(message, "purchase") ||
		strings.Contains(message, "vip")
}

func xiangjiaoEpisodeLocked(row map[string]any) bool {
	commerce := xiangjiaoMap(row, "commerce")
	if commerce == nil {
		commerce = row
	}
	if owned, ok := xiangjiaoBool(commerce, "owned"); ok && owned {
		return false
	}
	if canPlay, ok := xiangjiaoBool(commerce, "can_play", "canPlay"); ok {
		return !canPlay
	}
	charge := strings.ToLower(xiangjiaoText(commerce, "charge_type", "chargeType"))
	switch charge {
	case "", "free", "public":
		return false
	default:
		return true
	}
}

type xiangjiaoPlayableCacheEntry struct {
	playable bool
	expires  time.Time
}

var (
	xiangjiaoPlayableCache  sync.Map
	xiangjiaoGuestRefreshMu sync.Mutex
)

func xiangjiaoEpisodeRecord(row map[string]any) (string, int, bool) {
	explicitID := xiangjiaoText(row, "episode_id", "episodeId", "video_id", "videoId")
	id := firstNonEmpty(explicitID, xiangjiaoText(row, "id"))
	if id == "" {
		return "", 0, false
	}
	index := xiangjiaoInt(row, "episode_number", "episodeNumber", "episode_no", "episodeNo", "sort", "index")
	// 有些接口会在详情根对象上同时放 id、drama_id 和 episodes 数组。
	// 这种根对象不是一集，不能把整部剧的 ID 当成 episode_id。
	if _, hasNestedEpisodes := row["episodes"]; hasNestedEpisodes && index <= 0 && explicitID == "" {
		return "", 0, false
	}
	if index <= 0 && explicitID == "" && xiangjiaoText(row, "drama_id", "dramaId") == "" {
		return "", 0, false
	}
	return id, index, true
}

func (d *Downloader) xiangjiaoDramaFullyPlayable(ctx context.Context, sourceID string) bool {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return false
	}
	if cached, ok := xiangjiaoPlayableCache.Load(sourceID); ok {
		entry := cached.(xiangjiaoPlayableCacheEntry)
		if time.Now().Before(entry.expires) {
			return entry.playable
		}
		xiangjiaoPlayableCache.Delete(sourceID)
	}

	raw, err := d.xiangjiaoJSON(ctx, http.MethodGet, "/api/dramas/"+url.PathEscape(sourceID)+"/episodes", nil, "")
	if err != nil {
		return false
	}
	seen := map[string]bool{}
	var episodeIDs []string
	explicitlyLocked := false
	walkXiangjiaoMaps(xiangjiaoPayloadData(raw), func(row map[string]any) {
		id, _, ok := xiangjiaoEpisodeRecord(row)
		if !ok || seen[id] {
			return
		}
		seen[id] = true
		if xiangjiaoEpisodeLocked(row) {
			explicitlyLocked = true
		}
		episodeIDs = append(episodeIDs, id)
	})
	if len(episodeIDs) == 0 {
		return false
	}
	if explicitlyLocked {
		xiangjiaoPlayableCache.Store(sourceID, xiangjiaoPlayableCacheEntry{
			playable: false,
			expires:  time.Now().Add(2 * time.Minute),
		})
		return false
	}

	// The old code resolved every episode serially under a 12-second context.
	// A normal 20-episode drama could time out before its final episode and be
	// hidden even when every episode was playable. Probe with bounded parallelism.
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	var resultMu sync.Mutex
	failed := false
	paymentDenied := false
	for _, episodeID := range episodeIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-probeCtx.Done():
				return
			}
			if _, err := d.xiangjiaoFullPlaybackManifest(probeCtx, sourceID, id); err != nil {
				resultMu.Lock()
				failed = true
				if xiangjiaoPaymentRequiredFromError(err) {
					paymentDenied = true
				}
				resultMu.Unlock()
				cancel()
			}
		}(episodeID)
	}
	wg.Wait()
	resultMu.Lock()
	playable := !failed && probeCtx.Err() == nil
	shouldCacheDenied := paymentDenied
	resultMu.Unlock()
	if playable {
		xiangjiaoPlayableCache.Store(sourceID, xiangjiaoPlayableCacheEntry{
			playable: true,
			expires:  time.Now().Add(30 * time.Minute),
		})
	} else if shouldCacheDenied {
		// Cache only definite payment denials. Timeouts, mirror failures and
		// transient API errors are retried on the next catalog refresh.
		xiangjiaoPlayableCache.Store(sourceID, xiangjiaoPlayableCacheEntry{
			playable: false,
			expires:  time.Now().Add(2 * time.Minute),
		})
	}
	return playable
}

func (d *Downloader) filterXiangjiaoFullyPlayable(ctx context.Context, items []Drama) []Drama {
	if len(items) == 0 {
		return items
	}
	allowed := make([]bool, len(items))
	sem := make(chan struct{}, 3)
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
			probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			allowed[index] = d.xiangjiaoDramaFullyPlayable(probeCtx, items[index].SourceID)
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

func xiangjiaoEpisodeMarker(episodeID string) string {
	return "xiangjiao-play://episode/" + url.PathEscape(strings.TrimSpace(episodeID))
}

func xiangjiaoMarkerEpisode(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "xiangjiao-play" || parsed.Host != "episode" {
		return ""
	}
	value, _ := url.PathUnescape(strings.Trim(parsed.Path, "/"))
	return strings.TrimSpace(value)
}

func (d *Downloader) fetchXiangjiaoDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return Drama{}, nil, fmt.Errorf("香蕉剧集 ID 为空")
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, 20*time.Second)
	fullyPlayable := d.xiangjiaoDramaFullyPlayable(probeCtx, sourceID)
	probeCancel()
	if !fullyPlayable {
		return Drama{}, nil, fmt.Errorf("香蕉该剧未通过全集正片校验，已隐藏")
	}
	detailRaw, err := d.xiangjiaoJSON(ctx, http.MethodGet, "/api/dramas/"+url.PathEscape(sourceID), nil, "")
	if err != nil {
		return Drama{}, nil, err
	}
	episodesRaw, err := d.xiangjiaoJSON(ctx, http.MethodGet, "/api/dramas/"+url.PathEscape(sourceID)+"/episodes", nil, "")
	if err != nil {
		return Drama{}, nil, err
	}

	var drama Drama
	walkXiangjiaoMaps(xiangjiaoPayloadData(detailRaw), func(row map[string]any) {
		if drama.ID == "" {
			candidate := xiangjiaoDramaFromMap(row)
			if candidate.SourceID == sourceID || candidate.SourceID == "" {
				drama = candidate
			}
		}
	})
	if drama.ID == "" {
		drama = Drama{ID: providerDramaID(sourceXiangjiao, sourceID), Source: sourceXiangjiao, SourceID: sourceID, Title: "香蕉 " + sourceID, Name: "香蕉 " + sourceID, ChannelName: "香蕉"}
	}

	type episodeRow struct {
		id     string
		index  int
		title  string
		locked bool
	}
	seen := map[string]bool{}
	var episodes []episodeRow
	walkXiangjiaoMaps(xiangjiaoPayloadData(episodesRaw), func(row map[string]any) {
		id, index, ok := xiangjiaoEpisodeRecord(row)
		if !ok || seen[id] {
			return
		}
		seen[id] = true
		episodes = append(episodes, episodeRow{
			id:     id,
			index:  index,
			title:  xiangjiaoText(row, "title", "name", "episodeTitle", "episode_title"),
			locked: xiangjiaoEpisodeLocked(row),
		})
	})
	if len(episodes) == 0 {
		return Drama{}, nil, fmt.Errorf("香蕉详情未返回分集")
	}
	for index := range episodes {
		if episodes[index].index <= 0 {
			episodes[index].index = index + 1
		}
	}
	sort.SliceStable(episodes, func(i, j int) bool { return episodes[i].index < episodes[j].index })
	base := d.providerBaseURL(sourceXiangjiao)
	chapters := make([]Chapter, 0, len(episodes))
	freeFound := false
	for _, episode := range episodes {
		title := strings.TrimSpace(episode.title)
		if title == "" {
			title = fmt.Sprintf("第%d集", episode.index)
		}
		chapter := Chapter{
			VIP:            episode.locked,
			ID:             providerChapterID(sourceXiangjiao, sourceID, episode.id),
			Source:         sourceXiangjiao,
			Title:          title,
			PageURL:        strings.TrimRight(base, "/") + "/dramas/" + url.PathEscape(sourceID),
			Referer:        strings.TrimRight(base, "/") + "/",
			CurrentEpisode: rawEpisode(episode.index),
		}
		chapter.VideoURL = xiangjiaoEpisodeMarker(episode.id)
		if !episode.locked {
			freeFound = true
		}
		chapters = append(chapters, chapter)
	}
	vip := !freeFound
	drama.VIP = &vip
	drama.EpisodeCount = strconv.Itoa(len(chapters))
	drama.TotalEpisode = strconv.Itoa(len(chapters))
	return drama, chapters, nil
}

func newXiangjiaoDeviceID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		data[0:4], data[4:6], data[6:8], data[8:10], data[10:16]), nil
}

func (d *Downloader) xiangjiaoGuestToken(ctx context.Context, force bool) (string, error) {
	xiangjiaoGuestRefreshMu.Lock()
	defer xiangjiaoGuestRefreshMu.Unlock()
	d.xiangjiaoSessionMu.Lock()
	if !force && d.xiangjiaoAccessToken != "" && time.Now().Add(time.Minute).Before(d.xiangjiaoAccessExpires) {
		token := d.xiangjiaoAccessToken
		d.xiangjiaoSessionMu.Unlock()
		return token, nil
	}
	deviceID := d.xiangjiaoDeviceID
	d.xiangjiaoSessionMu.Unlock()
	if deviceID == "" {
		var err error
		deviceID, err = newXiangjiaoDeviceID()
		if err != nil {
			return "", err
		}
	}

	decoded, err := d.xiangjiaoJSON(ctx, http.MethodPost, "/api/guest-sessions", map[string]any{"device_id": deviceID}, "")
	if err != nil {
		return "", err
	}
	data, _ := xiangjiaoPayloadData(decoded).(map[string]any)
	token := xiangjiaoText(data, "access_token", "accessToken")
	if token == "" {
		return "", fmt.Errorf("香蕉游客会话未返回 access_token")
	}
	expires := time.Now().Add(xiangjiaoSessionTTL)
	if raw := xiangjiaoText(data, "expires_at", "expiresAt"); raw != "" {
		if parsed, parseErr := time.Parse(time.RFC3339, raw); parseErr == nil {
			expires = parsed
		}
	}
	d.xiangjiaoSessionMu.Lock()
	d.xiangjiaoDeviceID = deviceID
	d.xiangjiaoAccessToken = token
	d.xiangjiaoAccessExpires = expires
	d.xiangjiaoSessionMu.Unlock()
	return token, nil
}

func (d *Downloader) clearXiangjiaoGuestToken() {
	d.xiangjiaoSessionMu.Lock()
	d.xiangjiaoAccessToken = ""
	d.xiangjiaoAccessExpires = time.Time{}
	d.xiangjiaoSessionMu.Unlock()
}

func (d *Downloader) xiangjiaoFullPlaybackManifest(ctx context.Context, dramaID, episodeID string) (string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := d.xiangjiaoGuestToken(ctx, attempt > 0)
		if err != nil {
			return "", err
		}
		payload := map[string]any{"episode_id": episodeID}
		if strings.TrimSpace(dramaID) != "" {
			payload["drama_id"] = strings.TrimSpace(dramaID)
		}
		decoded, err := d.xiangjiaoJSON(ctx, http.MethodPost, "/api/playback/sessions", payload, token)
		if err != nil {
			if apiErr, ok := err.(*xiangjiaoHTTPError); ok {
				if apiErr.Status == http.StatusUnauthorized && attempt == 0 {
					d.clearXiangjiaoGuestToken()
					continue
				}
				if xiangjiaoPaymentRequired(apiErr) {
					return "", fmt.Errorf("香蕉该集需要会员或购买，服务端拒绝游客正片播放")
				}
			}
			return "", err
		}
		data, _ := xiangjiaoPayloadData(decoded).(map[string]any)
		mode := strings.ToLower(xiangjiaoText(data, "play_mode", "playMode"))
		if mode != "full" {
			return "", fmt.Errorf("香蕉服务端未返回 full 正片会话")
		}
		media := xiangjiaoMap(data, "media")
		manifest := xiangjiaoText(media, "manifest_url", "manifestUrl", "url")
		if manifest == "" {
			return "", fmt.Errorf("香蕉正片会话未返回 manifest_url")
		}
		if strings.HasPrefix(manifest, "/") {
			manifest = strings.TrimRight(d.providerBaseURL(sourceXiangjiao), "/") + manifest
		}
		return manifest, nil
	}
	return "", fmt.Errorf("香蕉游客会话刷新失败")
}

func (d *Downloader) resolveXiangjiaoPlayback(ctx context.Context, task Task) (providerMedia, error) {
	episodeID := xiangjiaoMarkerEpisode(task.Chapter.VideoURL)
	if episodeID == "" {
		return providerMedia{}, fmt.Errorf("香蕉分集播放标识无效，请刷新详情")
	}
	_, dramaID, validDrama := splitProviderDramaID(task.DramaID)
	if !validDrama {
		dramaID = ""
	}
	manifest, err := d.xiangjiaoFullPlaybackManifest(ctx, dramaID, episodeID)
	if err != nil {
		return providerMedia{}, err
	}
	return providerMedia{URL: manifest, Referer: firstNonEmpty(task.Chapter.Referer, d.providerBaseURL(sourceXiangjiao)+"/")}, nil
}
