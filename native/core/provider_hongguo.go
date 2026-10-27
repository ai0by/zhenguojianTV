package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const hongguoBaseURL = "https://hongguoduanju.com"

func (d *Downloader) fetchHongguoDramas(ctx context.Context) ([]Drama, error) {
	initialized := hongguoCatalogInitialized(d.hongguoCatalogSnapshot())
	dramas, appErr := d.fetchHongguoAppCatalog(ctx)
	if len(dramas) > 0 || appErr == nil && initialized {
		return dramas, appErr
	}
	if ctx.Err() != nil {
		return dramas, ctx.Err()
	}
	webDramas, webErr := d.fetchHongguoWebDramas(ctx)
	if len(webDramas) > 0 && webErr == nil {
		return webDramas, nil
	}
	return webDramas, errors.Join(appErr, webErr)
}

func (d *Downloader) fetchHongguoWebDramas(ctx context.Context) ([]Drama, error) {
	routes := []struct {
		path string
		name string
	}{
		{path: "real-drama", name: "真人剧"},
		{path: "comic-drama", name: "漫剧"},
		{path: "ai-drama", name: "AI剧"},
		{path: "comic", name: "动漫"},
	}
	type routeResult struct {
		dramas []Drama
		err    error
	}
	results := make(chan routeResult, len(routes))
	for _, route := range routes {
		route := route
		go func() {
			dramas, err := d.fetchHongguoRoute(ctx, route.path, route.name)
			results <- routeResult{dramas: dramas, err: err}
		}()
	}
	seen := map[string]bool{}
	var out []Drama
	var failures []error
	for range routes {
		result := <-results
		if result.err != nil {
			failures = append(failures, result.err)
		}
		for _, drama := range result.dramas {
			if drama.ID != "" && !seen[drama.ID] {
				seen[drama.ID] = true
				out = append(out, drama)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DisplayTitle() < out[j].DisplayTitle() })
	return out, errors.Join(failures...)
}

func (d *Downloader) fetchHongguoRoute(ctx context.Context, route, category string) ([]Drama, error) {
	pages := d.cfg.MaxPagesPerSort
	if pages < 1 {
		pages = 1
	}
	var dramas []Drama
	seen := map[string]bool{}
	for page := 1; page <= pages; page++ {
		items, totalPages, err := d.fetchHongguoCategoryPage(ctx, route+"?page="+strconv.Itoa(page), category)
		if err != nil {
			return dramas, fmt.Errorf("%s: %w", category, err)
		}
		if totalPages > pages {
			pages = totalPages
			if pages > 500 {
				pages = 500
			}
		}
		reportLibraryProgress(ctx, sourceHongguo, items, nil, false)
		added := 0
		for _, drama := range items {
			if !seen[drama.ID] {
				seen[drama.ID] = true
				dramas = append(dramas, drama)
				added++
			}
		}
		if added == 0 || len(items) < 24 {
			break
		}
	}
	return dramas, nil
}

func (d *Downloader) fetchHongguoCategoryPage(ctx context.Context, route, category string) ([]Drama, int, error) {
	body, err := d.fetchProviderText(ctx, hongguoBaseURL+"/category/"+route, hongguoBaseURL+"/")
	if err != nil {
		return nil, 0, err
	}
	data := parseRouterData(body)
	page := routerLoaderMap(data, "category_page", "category_$")
	if len(page) == 0 || page["isSuccess"] == false {
		return nil, 0, errors.New("红果分类数据不可用，可能是页面结构或访问权限变化")
	}
	items := anyList(page["recommendList"])
	out := make([]Drama, 0, len(items))
	for _, item := range items {
		if dr := hongguoDramaFromAny(item, category); dr.ID != "" {
			out = append(out, dr)
		}
	}
	pages, _ := strconv.Atoi(mapString(nestedMap(page, "pagination"), "totalPages"))
	return out, pages, nil
}

func (d *Downloader) fetchHongguoChapters(ctx context.Context, sourceID string) (string, []Chapter, error) {
	sourceID = strings.TrimPrefix(strings.TrimSpace(sourceID), "hg-series-v1:")
	if sourceID == "" {
		return "", nil, fmt.Errorf("empty hongguo sourceID")
	}
	entry, appErr := d.hongguoAppDetail(ctx, sourceID)
	if appErr == nil {
		return entry.Drama.DisplayTitle(), append([]Chapter(nil), entry.Chapters...), nil
	}
	if ctx.Err() != nil {
		return "", nil, ctx.Err()
	}
	title, chapters, webErr := d.fetchHongguoWebChapters(ctx, sourceID)
	if webErr == nil {
		return title, chapters, nil
	}
	return "", nil, fmt.Errorf("App 详情失败: %v；网页详情失败: %w", publicError(appErr), webErr)
}

func (d *Downloader) fetchHongguoWebChapters(ctx context.Context, sourceID string) (string, []Chapter, error) {
	body, err := d.fetchProviderText(ctx, hongguoBaseURL+"/detail?series_id="+url.QueryEscape(sourceID), hongguoBaseURL+"/")
	if err != nil {
		return "", nil, err
	}
	data := parseRouterData(body)
	page := routerLoaderMap(data, "detail_page", "detail_")
	detail, _ := page["seriesDetail"].(map[string]any)
	if len(detail) == 0 {
		return "", nil, fmt.Errorf("hongguo detail is empty")
	}
	title := firstNonEmpty(mapString(detail, "series_name", "series_title", "name"), sourceID)
	vids := anyList(detail["vid_list"])
	chapters := make([]Chapter, 0, len(vids))
	for i, v := range vids {
		vid := strings.TrimSpace(fmt.Sprint(v))
		if vid == "" || vid == "<nil>" {
			continue
		}
		idx := i + 1
		chapters = append(chapters, Chapter{ID: providerChapterID(sourceHongguo, sourceID, vid), Source: sourceHongguo, Title: fmt.Sprintf("第%d集", idx), VideoURL: "hongguo-cenc://" + vid, CurrentEpisode: rawEpisode(idx)})
	}
	if len(chapters) == 0 {
		return title, nil, errors.New("红果详情没有返回剧集 ID")
	}
	return title, chapters, nil
}

func parseRouterData(raw string) map[string]any {
	idx := regexp.MustCompile(`(?s)(?:window\.)?_ROUTER_DATA\s*=\s*`).FindStringIndex(raw)
	if idx == nil {
		return nil
	}
	var data map[string]any
	dec := json.NewDecoder(strings.NewReader(raw[idx[1]:]))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		return nil
	}
	return data
}

func (d *Downloader) resolveHongguoMedia(ctx context.Context, task Task) (providerMedia, error) {
	source, seriesID, ok := splitProviderDramaID(task.DramaID)
	if !ok || source != sourceHongguo {
		return providerMedia{}, fmt.Errorf("红果章节缺少剧 ID，请刷新剧库后重新加入队列")
	}
	videoID := strings.TrimPrefix(task.Chapter.VideoURL, "hongguo-cenc://")
	if !hongguoNumericID.MatchString(videoID) || !hongguoNumericID.MatchString(seriesID) {
		return providerMedia{}, fmt.Errorf("红果章节 ID 无效，请重新获取章节")
	}
	media, nativeErr := d.resolveHongguoAppMedia(ctx, videoID)
	if nativeErr == nil {
		return media, nil
	}
	if err := ctx.Err(); err != nil {
		return providerMedia{}, err
	}
	media, publicErr := d.resolveHongguoPublicMedia(ctx, videoID)
	if publicErr == nil {
		return media, nil
	}
	if err := ctx.Err(); err != nil {
		return providerMedia{}, err
	}
	media, pageErr := d.resolveHongguoWebMedia(ctx, seriesID, videoID)
	if pageErr == nil {
		return media, nil
	}
	// 原先这里还有一级第三方解析（djapi.999888456.xyz）。实测该接口
	// 解密后固定返回 {"parse":0,"url":""}，多剧多轮均无地址，已移除，
	// 避免用户等待一个必然失败的请求。
	//
	// 0.8.14：三条链路都失败时把各级原因摊开。App 链路是主链路，它失败
	// 通常是设备注册没成功或该集确实没有地址。
	return providerMedia{}, fmt.Errorf(
		"红果取不到该集播放地址（App：%v；公开片源：%v；网页：%v）",
		publicError(nativeErr), publicError(publicErr), pageErr)
}

// resolveHongguoPublicMedia 使用红果网页播放器同款的公开片源。
//
// 站点前端把公开集映射到 /video/public/{vid}/360p.mp4（见
// player 的 "/video/public/" 分支），该地址不带签名、不带 Cookie，返回
// video/mp4 且支持 Range。
//
// **0.8.14 起这里只作为最后兜底，而且必须校验时长**：2026-10-27 实测，
// 这个 360p 已经从"完整正片"换成了**固定 10.00 秒的预告**（mvhd
// duration=10000 / timescale=1000），旧链路把它当正片返回，于是每集只播
// 10 秒就结束。现在先读 64KB 头部判断真实时长，明显短于正片的一律拒收。
func (d *Downloader) resolveHongguoPublicMedia(ctx context.Context, videoID string) (providerMedia, error) {
	if !hongguoNumericID.MatchString(videoID) {
		return providerMedia{}, errors.New("红果视频 ID 无效")
	}
	bases := d.providerBaseCandidates(ctx, sourceHongguo, []string{hongguoBaseURL})
	var lastErr error
	for _, base := range bases {
		if err := ctx.Err(); err != nil {
			return providerMedia{}, err
		}
		address := base + "/video/public/" + url.PathEscape(videoID) + "/360p.mp4"
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			lastErr = err
			continue
		}
		request.Header.Set("User-Agent", userAgent)
		// 多读一点：要顺带从 mp4 头部判断时长（见 hongguoPreviewMaxDuration）。
		request.Header.Set("Range", "bytes=0-65535")
		response, err := d.doCatalogRequestWithTimeout(request, providerTimeout)
		if err != nil {
			lastErr = err
			continue
		}
		if response.StatusCode != http.StatusPartialContent && response.StatusCode != http.StatusOK {
			response.Body.Close()
			lastErr = fmt.Errorf("红果公开片源不可用（HTTP %d）", response.StatusCode)
			continue
		}
		contentType := strings.ToLower(response.Header.Get("Content-Type"))
		if !strings.Contains(contentType, "video/") && !strings.Contains(contentType, "mp4") {
			response.Body.Close()
			lastErr = errors.New("红果公开片源返回的不是视频内容")
			continue
		}
		prefix, readErr := io.ReadAll(io.LimitReader(response.Body, hongguoMediaProbeBytes))
		response.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		duration, durationErr := hongguoMediaDurationFromPrefix(prefix)
		if durationErr != nil {
			// 读不出时长就按"未知"处理，不因此误伤正常片源。
			duration = 0
		}
		if hongguoLooksLikePreview(duration) {
			lastErr = fmt.Errorf("红果公开片源只有 %.0f 秒预告，不是正片", duration.Seconds())
			continue
		}
		d.rememberProviderHost(sourceHongguo, base)
		return providerMedia{URL: address, Referer: "", Quality: 360, Duration: duration}, nil
	}
	if lastErr == nil {
		lastErr = errors.New("红果公开片源没有可用域名")
	}
	return providerMedia{}, lastErr
}

// resolveHongguoWebMedia 走网页播放页（服务端渲染）。
//
// 这是兜底链路：红果的播放页由服务端渲染，匿名访问时
// `protectedPlayback: true`、`videoList` 为空，前端 JS 只能拼出 10 秒预告，
// 所以正常情况下拿不到正片 —— 主链路是 App 取流（见 resolveHongguoAppMedia）。
// 这里保留是因为站点偶尔会放开部分剧集，命中时能多一条路。
func (d *Downloader) resolveHongguoWebMedia(ctx context.Context, seriesID, videoID string) (providerMedia, error) {
	pageURL := hongguoBaseURL + "/player/" + url.PathEscape(seriesID) + "/" + url.PathEscape(videoID)
	body, err := d.fetchProviderText(ctx, pageURL, hongguoBaseURL+"/")
	if err != nil {
		return providerMedia{}, err
	}
	page := routerLoaderMap(parseRouterData(body), "player_", "player_page")
	if vid := mapString(page, "vid"); vid != "" && vid != videoID {
		return providerMedia{}, errors.New("红果未返回所请求的剧集")
	}
	info, _ := page["video_player_info"].(map[string]any)
	addresses := hongguoWebAddresses(page)
	if len(addresses) == 0 {
		return providerMedia{}, errors.New("红果网页未提供播放地址（未登录时只放预告）")
	}
	duration, _ := strconv.ParseFloat(hongguoWebDuration(page, info), 64)
	// 与公开片源同一套判断：不足 20 秒说明拿到的还是预告，不能当正片播。
	if hongguoLooksLikePreview(time.Duration(duration * float64(time.Second))) {
		return providerMedia{}, fmt.Errorf("红果网页只返回了 %.0f 秒预告", duration)
	}
	media := providerMedia{URL: addresses[0], Referer: d.providerBaseURL(sourceHongguo) + "/", Duration: time.Duration(duration * float64(time.Second))}
	for _, address := range addresses {
		variant := media
		variant.URL, variant.Variants = address, nil
		media.Variants = append(media.Variants, variant)
	}
	return media, nil
}

// hongguoWebAddresses 从播放页里收集片源地址。
//
// 匿名时只有 `video_player_info`（通常为空）；站点放开时真实片源会出现在
// `videoList`（清晰度 → 片源对象）。两处都扫，任一命中即可。
func hongguoWebAddresses(page map[string]any) []string {
	var addresses []string
	seen := map[string]bool{}
	collect := func(info map[string]any) {
		if len(info) == 0 {
			return
		}
		for _, address := range hongguoMediaAddresses(info) {
			if seen[address] {
				continue
			}
			seen[address] = true
			addresses = append(addresses, address)
		}
	}
	info, _ := page["video_player_info"].(map[string]any)
	collect(info)
	switch list := page["videoList"].(type) {
	case map[string]any:
		for _, value := range list {
			entry, _ := value.(map[string]any)
			collect(entry)
		}
	case []any:
		for _, value := range list {
			entry, _ := value.(map[string]any)
			collect(entry)
		}
	}
	return addresses
}

// hongguoWebDuration 取播放页里声明的片长（秒，字符串形式）。
func hongguoWebDuration(page map[string]any, info map[string]any) string {
	if value := mapString(info, "duration", "video_duration"); value != "" {
		return value
	}
	switch list := page["videoList"].(type) {
	case map[string]any:
		for _, value := range list {
			if entry, ok := value.(map[string]any); ok {
				if duration := mapString(entry, "duration", "video_duration"); duration != "" {
					return duration
				}
			}
		}
	case []any:
		for _, value := range list {
			if entry, ok := value.(map[string]any); ok {
				if duration := mapString(entry, "duration", "video_duration"); duration != "" {
					return duration
				}
			}
		}
	}
	return mapString(page, "duration", "video_duration")
}

func hongguoDramaFromAny(v any, category string) Drama {
	m, ok := v.(map[string]any)
	if !ok {
		return Drama{}
	}
	vd, _ := m["video_data"].(map[string]any)
	if len(vd) == 0 {
		vd = m
	}
	sourceID := firstNonEmpty(mapString(vd, "series_id_str", "series_id"), mapString(m, "series_id_str", "series_id"), mapString(vd, "keyword"), mapString(m, "keyword"))
	if !hongguoNumericID.MatchString(sourceID) {
		return Drama{}
	}
	title := firstNonEmpty(mapString(vd, "series_title", "series_name", "title"), mapString(m, "series_name", "name"), sourceID)
	cover := firstNonEmpty(mapString(vd, "series_cover", "cover"), mapString(m, "series_cover"))
	intro := firstNonEmpty(mapString(vd, "series_intro", "video_desc"), mapString(m, "series_intro"))
	count := firstNonEmpty(mapString(vd, "episode_cnt"), mapString(m, "episode_cnt"))
	remark := firstNonEmpty(mapString(vd, "episode_right_text"), mapString(m, "episode_right_text"))
	releaseStatus := releaseStatusFromRemark(remark)
	if mapString(vd, "series_status") == "1" {
		releaseStatus = "finished"
	} else if mapString(vd, "series_status") == "0" {
		releaseStatus = "ongoing"
	}
	if remark == "" && count != "" {
		remark = "共" + count + "集"
	}
	tags := mapStringSlice(vd, "tags")
	for _, value := range anyList(vd["category_list"]) {
		item, _ := value.(map[string]any)
		if name := mapString(item, "name"); name != "" && !slices.Contains(tags, name) {
			tags = append(tags, name)
		}
	}
	var categories []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(mapString(vd, "category_schema")), &categories) == nil {
		for _, item := range categories {
			if item.Name != "" && !slices.Contains(tags, item.Name) {
				tags = append(tags, item.Name)
			}
		}
	}
	genre := mapString(vd, "category_name", "categoryName", "category")
	if genre == "" && len(tags) > 0 {
		genre = tags[0]
	}
	return Drama{ID: providerDramaID(sourceHongguo, sourceID), Source: sourceHongguo, SourceID: sourceID, Title: title, Name: title, Desc: intro, Intro: intro, Cover: cover, CoverURL: cover, CategoryName: firstNonEmpty(genre, category), ChannelName: "红果", Remark: remark, TotalEpisode: count, EpisodeCount: count, Tags: tags, ReleaseStatus: releaseStatus, Score: mapString(vd, "score"), Views: mapString(vd, "series_play_cnt", "play_cnt"), Heat: hongguoHeat(vd), OnlineDate: hongguoOnlineDate(vd, time.Now())}
}

func nestedMap(v any, keys ...string) map[string]any {
	cur, _ := v.(map[string]any)
	for _, key := range keys {
		if cur == nil {
			return nil
		}
		cur, _ = cur[key].(map[string]any)
	}
	return cur
}

func routerLoaderMap(data map[string]any, names ...string) map[string]any {
	loader, _ := data["loaderData"].(map[string]any)
	for _, name := range names {
		if page, _ := loader[name].(map[string]any); len(page) > 0 {
			return page
		}
	}
	for key, value := range loader {
		for _, name := range names {
			if strings.TrimSuffix(name, "$") != "" && strings.HasPrefix(key, strings.TrimSuffix(name, "$")) {
				if page, _ := value.(map[string]any); len(page) > 0 {
					return page
				}
			}
		}
	}
	return nil
}

func anyList(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case map[string]any:
		for _, key := range []string{"list", "items", "data"} {
			if out := anyList(x[key]); len(out) > 0 {
				return out
			}
		}
	}
	return nil
}
