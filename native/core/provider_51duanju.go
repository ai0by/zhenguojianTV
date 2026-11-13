package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"
)

// 51短剧（0.8.30 修复）：站点在 2026-11 从 Nuxt 3 改成了服务端渲染的
// `hg-*` 模板，旧的 /rank/、/new-theater-category/、/play/<id>/1/ 全部 404，
// `__NUXT_DATA__` 也不再输出。现在的形态是：
//
//   - 列表：`hg-drama-card`（首页 / 分类 / 搜索）与 `hg-rank-item`（榜单），
//     剧集 ID 在 `data-track-id`，封面用 `img[data-src]`（src 是占位图）；
//   - 分类：`/`、`/ranks/hot/`、`/newest/`、`/ai-duanju/` 等，分页是
//     `/ai-duanju/<n>/` 这种路径式；搜索是 `/search/?keyword=...`；
//   - 详情：`<script id="videoInitialData">` 里是普通 JSON，全部分集由
//     `.hg-web-play__ep[data-ep-id]` 给出，剧集页是 `/video/<id>/ep-<n>/`；
//   - 播放：每集 m3u8 带**有时效的 auth_key**，且 `epPlaySrcs` 只是当前集
//     附近的滑动窗口。所以详情里只存 `51duanju-play://<id>/<n>` 占位，
//     真正播放时再抓对应集页面换出新鲜地址。
//
// 页面里还塞了防爬脚本与隐藏的「猜你喜欢」卡片；解析统一走 DOM（复用
// provider_maccms_html.go 里的通用工具），并只取 `<main>` / 当前激活的
// `hg-card-grid` 子树，避免把 SEO 模板和隐藏面板混进目录。
const (
	sourceDuanju51     = "51duanju"
	duanju51BaseURL    = "https://blue.xdrgcewe.cc"
	duanju51PlayScheme = "51duanju-play://"
)

// 镜像池与 config/source_mirrors.json 的 51duanju 段保持一致。
// 该站换域名很勤，新地址优先改远程配置，这份内置列表只作兜底。
var duanju51BaseMirrors = []string{
	"https://blue.xdrgcewe.cc",
}

var duanju51Categories = []nativeCategory{
	{ID: "home", Name: "首页推荐"},
	{ID: "rank", Name: "热播榜"},
	{ID: "theater", Name: "AI 短剧"},
	{ID: "new", Name: "最新更新"},
	{ID: "manju", Name: "AI 漫剧"},
	{ID: "huanlian", Name: "AI 换脸"},
	{ID: "mogai", Name: "AI 魔改"},
}

// 分类 ID → 站点路径。首页与榜单只有一页，其余是 /<route>/<n>/ 分页。
var duanju51CatalogRoutes = map[string]string{
	"home":     "/",
	"rank":     "/ranks/hot/",
	"theater":  "/ai-duanju/",
	"new":      "/newest",
	"manju":    "/ai-manju/",
	"huanlian": "/ai-huanlian/",
	"mogai":    "/ai-mogai/",
}

var (
	duanju51VideoPattern = regexp.MustCompile(`/video/([0-9]+)/`)
	duanju51DigitPattern = regexp.MustCompile(`[0-9]+`)
)

func duanju51MirrorHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, origin := range duanju51BaseMirrors {
		if strings.TrimPrefix(strings.ToLower(origin), "https://") == host {
			return true
		}
	}
	return false
}

func duanju51NumericID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func validDuanju51Category(category string) bool {
	_, ok := duanju51CatalogRoutes[category]
	return ok
}

// duanju51CatalogPath 返回分类列表页路径。首页 / 榜单只有一页，page>1 返回空。
func duanju51CatalogPath(category string, page int) string {
	if page < 1 {
		page = 1
	}
	route, ok := duanju51CatalogRoutes[category]
	if !ok {
		route = duanju51CatalogRoutes["theater"]
	}
	if route == "/" || route == "/ranks/hot/" {
		if page > 1 {
			return ""
		}
		return route
	}
	name := strings.Trim(route, "/")
	if page == 1 {
		return "/" + name + "/"
	}
	return fmt.Sprintf("/%s/%d/", name, page)
}

// duanju51SearchPath 用页面搜索框的 keyword 参数；page 直接透传，站点只有
// 一页时第二页会回空列表（由 duanju51HasNextPage 控制不再翻页）。
func duanju51SearchPath(query string, page int) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}
	if page < 1 {
		page = 1
	}
	path := "/search/?keyword=" + url.QueryEscape(query)
	if page > 1 {
		path += "&page=" + strconv.Itoa(page)
	}
	return path
}

// duanju51DetailPath 详情页路径：第 1 集是 /video/<id>/，其余是 ep-<n>。
func duanju51DetailPath(sourceID string, episode int) string {
	id := url.PathEscape(strings.Trim(strings.TrimSpace(sourceID), "/"))
	if id == "" {
		return ""
	}
	if episode <= 1 {
		return "/video/" + id + "/"
	}
	return fmt.Sprintf("/video/%s/ep-%d/", id, episode)
}

func duanju51PlayPlaceholder(sourceID string, episode int) string {
	return duanju51PlayScheme + strings.TrimSpace(sourceID) + "/" + strconv.Itoa(episode)
}

func duanju51ParsePlayPlaceholder(raw string) (string, int, bool) {
	value := strings.Trim(strings.TrimPrefix(strings.TrimSpace(raw), duanju51PlayScheme), "/")
	parts := strings.Split(value, "/")
	if len(parts) != 2 || !duanju51NumericID(parts[0]) {
		return "", 0, false
	}
	episode, err := strconv.Atoi(parts[1])
	if err != nil || episode < 1 {
		return "", 0, false
	}
	return parts[0], episode, true
}

func (d *Downloader) fetchDuanju51Page(ctx context.Context, path string) (string, error) {
	base := d.providerBaseURL(sourceDuanju51)
	return d.fetchProviderText(ctx, strings.TrimRight(base, "/")+path, base+"/")
}

// fetchDuanju51CatalogPage 抓取列表页并解析卡片。query 非空时走站内搜索。
func (d *Downloader) fetchDuanju51CatalogPage(ctx context.Context, page int, category, query string) ([]Drama, bool, error) {
	path := ""
	if strings.TrimSpace(query) != "" {
		path = duanju51SearchPath(query, page)
	} else {
		path = duanju51CatalogPath(category, page)
	}
	if path == "" {
		return nil, false, nil
	}
	body, err := d.fetchDuanju51Page(ctx, path)
	if err != nil {
		return nil, false, err
	}
	items := duanju51CardDramas(body)
	// 分页以页面里的「下一页」链接为准，避免最后一页翻出 404。
	more := len(items) > 0 && duanju51HasNextPage(body, page)
	return items, more, nil
}

// fetchDuanju51Detail 取详情元信息与全部分集；分集只存占位，播放时再解析。
func (d *Downloader) fetchDuanju51Detail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	sourceID = strings.Trim(strings.TrimSpace(sourceID), "/")
	if !duanju51NumericID(sourceID) {
		return Drama{}, nil, fmt.Errorf("51短剧剧集 ID 无效")
	}
	base := strings.TrimRight(d.providerBaseURL(sourceDuanju51), "/")
	body, err := d.fetchDuanju51Page(ctx, duanju51DetailPath(sourceID, 1))
	if err != nil {
		return Drama{}, nil, err
	}
	doc := maccmsDocument(body)
	data := duanju51InitialData(doc)
	if data == nil {
		return Drama{}, nil, fmt.Errorf("51短剧详情解析失败，站点结构可能已变更")
	}

	indexes := duanju51EpisodeIndexes(doc, data)
	if len(indexes) == 0 {
		return Drama{}, nil, fmt.Errorf("51短剧该剧暂无可用分集")
	}
	var chapters []Chapter
	for _, index := range indexes {
		path := duanju51DetailPath(sourceID, index)
		pageURL := base + path
		chapters = append(chapters, Chapter{
			ID:             providerChapterID(sourceDuanju51, sourceID, strconv.Itoa(index)),
			Source:         sourceDuanju51,
			Title:          fmt.Sprintf("第%d集", index),
			VideoURL:       duanju51PlayPlaceholder(sourceID, index),
			PageURL:        pageURL,
			Referer:        pageURL,
			CurrentEpisode: rawEpisode(index),
		})
	}
	sortProviderChapters(chapters)
	chapters = uniqueChapters(chapters)

	title := firstNonEmpty(mapString(data, "title"), "短剧"+sourceID)
	cover := duanju51AbsoluteURL(mapString(data, "coverSrc", "posterSrc"))
	description := mapString(data, "description")
	vip := false
	drama := Drama{
		VIP:          &vip,
		ID:           providerDramaID(sourceDuanju51, sourceID),
		Source:       sourceDuanju51,
		SourceID:     sourceID,
		Title:        title,
		Name:         title,
		Desc:         description,
		Intro:        description,
		Cover:        cover,
		CoverURL:     cover,
		TotalEpisode: strconv.Itoa(len(chapters)),
		EpisodeCount: strconv.Itoa(len(chapters)),
		ChannelName:  "51短剧",
		CategoryName: firstNonEmpty(duanju51BreadcrumbCategory(data), "短剧"),
		Remark:       fmt.Sprintf("全%d集", len(chapters)),
		Tags:         mapStringSlice(data, "tags"),
		Views:        normalizeViews(mapString(data, "views")),
	}
	return drama, chapters, nil
}

// resolveDuanju51Playback 按需抓「指定集」的详情页，换出带新鲜 auth_key 的 m3u8。
// 老版本保存的裸 m3u8（同样带 auth_key）也走这里，避免历史收藏里的链接过期。
func (d *Downloader) resolveDuanju51Playback(ctx context.Context, task Task) (providerMedia, error) {
	sourceID, episode, ok := duanju51ParsePlayPlaceholder(task.Chapter.VideoURL)
	if !ok {
		if source, dramaID, valid := splitProviderDramaID(task.DramaID); valid && source == sourceDuanju51 {
			sourceID = dramaID
		}
		fallback := task.Index
		if fallback < 1 {
			fallback = 1
		}
		episode, _ = strconv.Atoi(task.Chapter.EpisodeString(fallback))
		ok = duanju51NumericID(sourceID) && episode > 0
	}
	if !ok {
		return providerMedia{}, errors.New("51短剧播放分集信息无效，请刷新详情")
	}
	// DramaID 是权威来源：占位里的 ID 可能来自旧域名或旧快照。
	if source, dramaID, valid := splitProviderDramaID(task.DramaID); valid && source == sourceDuanju51 && duanju51NumericID(dramaID) {
		sourceID = dramaID
	}
	path := duanju51DetailPath(sourceID, episode)
	body, err := d.fetchDuanju51Page(ctx, path)
	if err != nil {
		return providerMedia{}, err
	}
	doc := maccmsDocument(body)
	data := duanju51InitialData(doc)
	if data == nil {
		return providerMedia{}, fmt.Errorf("51短剧第%d集页面结构异常，站点可能已改版", episode)
	}
	address := duanju51EpisodeMedia(doc, data, episode)
	if !isProviderHTTPMediaURL(address) {
		return providerMedia{}, fmt.Errorf("51短剧第%d集暂未返回可用播放地址", episode)
	}
	pageURL := strings.TrimRight(d.providerBaseURL(sourceDuanju51), "/") + path
	// 只负责把地址换出来；m3u8 拉取、#EXTM3U 校验与时长解析交给公共尾部。
	return providerMedia{URL: address, Referer: pageURL}, nil
}

// duanju51EpisodeMedia 优先取当前集对应的 epPlaySrcs[n]，再退到 videoSrc 与
// `<video><source>`。三者实测是同一个地址，但滑动窗口不一定包含较大集数。
func duanju51EpisodeMedia(doc *xhtml.Node, data map[string]any, episode int) string {
	if sources, ok := data["epPlaySrcs"].(map[string]any); ok {
		if address := mapString(sources, strconv.Itoa(episode)); isProviderHTTPMediaURL(address) {
			return address
		}
	}
	if address := mapString(data, "videoSrc", "previewSrc"); isProviderHTTPMediaURL(address) {
		return address
	}
	if video := maccmsFind(doc, func(node *xhtml.Node) bool {
		return maccmsTag(node, "video") && maccmsHasClass(node, "hg-web-play__video")
	}); video != nil {
		for _, source := range maccmsNodes(video, func(node *xhtml.Node) bool {
			return maccmsTag(node, "source")
		}) {
			if address := maccmsAttr(source, "src"); isProviderHTTPMediaURL(address) {
				return address
			}
		}
	}
	return ""
}

// duanju51InitialData 从 `<script id="videoInitialData">` 读普通 JSON。
func duanju51InitialData(doc *xhtml.Node) map[string]any {
	node := maccmsFind(doc, func(current *xhtml.Node) bool {
		return maccmsTag(current, "script") && strings.EqualFold(maccmsAttr(current, "id"), "videoInitialData")
	})
	if node == nil {
		return nil
	}
	var builder strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.TextNode {
			builder.WriteString(child.Data)
		}
	}
	text := strings.TrimSpace(builder.String())
	if text == "" {
		return nil
	}
	var data map[string]any
	if json.Unmarshal([]byte(text), &data) != nil {
		// script 是 raw text，实体引用不会被解析器还原；个别模板会手写 &amp;。
		if json.Unmarshal([]byte(html.UnescapeString(text)), &data) != nil {
			return nil
		}
	}
	return data
}

// duanju51EpisodeIndexes 从分集按钮取全部集号；按钮缺失时退到 JSON 里的
// 三张集号表，最后退到当前集号。
func duanju51EpisodeIndexes(doc *xhtml.Node, data map[string]any) []int {
	set := map[int]bool{}
	for _, node := range maccmsNodes(doc, func(current *xhtml.Node) bool {
		return maccmsHasClass(current, "hg-web-play__ep")
	}) {
		if index, err := strconv.Atoi(maccmsAttr(node, "data-ep-id")); err == nil && index > 0 {
			set[index] = true
		}
	}
	if len(set) == 0 {
		for _, key := range []string{"episodeDurationLabels", "trackingEpisodes", "epPlaySrcs"} {
			entries, ok := data[key].(map[string]any)
			if !ok {
				continue
			}
			for raw := range entries {
				if index, err := strconv.Atoi(raw); err == nil && index > 0 {
					set[index] = true
				}
			}
			if len(set) > 0 {
				break
			}
		}
	}
	if len(set) == 0 {
		if index, err := strconv.Atoi(mapString(data, "ep")); err == nil && index > 0 {
			set[index] = true
		}
	}
	indexes := make([]int, 0, len(set))
	for index := range set {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	return indexes
}

func duanju51BreadcrumbCategory(data map[string]any) string {
	entries, _ := data["breadcrumb"].([]any)
	for _, entry := range entries {
		if row, ok := entry.(map[string]any); ok {
			if name := mapString(row, "name"); name != "" && name != "51短剧" {
				return name
			}
		}
	}
	return ""
}

// duanju51HasNextPage 以分页器里的「页码 = 当前页 + 1」为准。
func duanju51HasNextPage(rawHTML string, page int) bool {
	doc := maccmsDocument(rawHTML)
	if doc == nil {
		return false
	}
	next := strconv.Itoa(page + 1)
	nodes := maccmsNodes(doc, func(current *xhtml.Node) bool {
		return maccmsTag(current, "a", "button", "span")
	})
	for _, node := range nodes {
		if maccmsAttr(node, "data-ajax-go") == next {
			return true
		}
		if pages := maccmsAttr(node, "data-pages"); pages != "" {
			if total, err := strconv.Atoi(pages); err == nil && total > page {
				return true
			}
		}
		href := maccmsAttr(node, "href")
		if href == "" {
			continue
		}
		if strings.Contains(href, "/"+next+"/") ||
			strings.HasSuffix(href, "page="+next) ||
			strings.Contains(href, "page="+next+"&") {
			return true
		}
	}
	return false
}

// duanju51CardDramas 解析列表页 / 搜索页 / 榜单页的卡片。
func duanju51CardDramas(rawHTML string) []Drama {
	doc := maccmsDocument(rawHTML)
	if doc == nil {
		return nil
	}
	scope := doc
	// 分类页会为「最新/热播/原创/随机」预渲染四组卡片，只有 is-active 那组
	// 是当前页内容；首页 / 榜单 / 搜索页则直接取 <main>。
	if active := maccmsFind(doc, func(node *xhtml.Node) bool {
		return maccmsHasClass(node, "hg-card-grid") && maccmsHasClass(node, "is-active")
	}); active != nil {
		scope = active
	} else if main := maccmsFind(doc, func(node *xhtml.Node) bool {
		return maccmsTag(node, "main")
	}); main != nil {
		scope = main
	}
	cards := maccmsNodes(scope, func(node *xhtml.Node) bool {
		return maccmsHasClass(node, "hg-drama-card") || maccmsHasClass(node, "hg-rank-item")
	})
	var out []Drama
	seen := map[string]bool{}
	for _, card := range cards {
		if duanju51HiddenCard(card) {
			continue
		}
		drama, ok := duanju51CardDrama(card)
		if !ok || seen[drama.SourceID] {
			continue
		}
		seen[drama.SourceID] = true
		out = append(out, drama)
	}
	return out
}

// duanju51HiddenCard 排除 `<template>` 与页面顶部隐藏的搜索建议卡片。
func duanju51HiddenCard(node *xhtml.Node) bool {
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if maccmsTag(parent, "template") || maccmsHasClass(parent, "hg-search-suggest") {
			return true
		}
	}
	return false
}

func duanju51CardDrama(card *xhtml.Node) (Drama, bool) {
	videoID := maccmsAttr(card, "data-track-id")
	if videoID == "" {
		for _, anchor := range maccmsNodes(card, func(node *xhtml.Node) bool {
			return maccmsTag(node, "a")
		}) {
			if match := duanju51VideoPattern.FindStringSubmatch(maccmsAttr(anchor, "href")); len(match) == 2 {
				videoID = match[1]
				break
			}
		}
	}
	if !duanju51NumericID(videoID) {
		return Drama{}, false
	}

	title := maccmsAttr(card, "data-track-title")
	if title == "" {
		title = duanju51VisibleText(duanju51ClassFind(card, "hg-drama-card__title", "hg-rank-item__title"))
	}
	if title == "" {
		title = duanju51VisibleText(duanju51ClassFind(card, "hg-drama-card__cover", "hg-rank-item__cover"))
	}
	if title == "" {
		return Drama{}, false
	}

	var cover string
	for _, image := range maccmsNodes(card, func(node *xhtml.Node) bool {
		return maccmsTag(node, "img")
	}) {
		candidate := maccmsAttr(image, "data-src", "src")
		if candidate == "" || strings.Contains(candidate, "cover-placeholder") {
			continue
		}
		cover = duanju51AbsoluteURL(candidate)
		break
	}

	remark := duanju51VisibleText(duanju51ClassFind(card, "hg-drama-card__episode"))
	if remark == "" {
		if heat := duanju51VisibleText(duanju51ClassFind(card, "hg-rank-item__heat-value", "hg-rank-metric-value")); heat != "" {
			remark = "热度 " + heat
		}
	}
	episodes := ""
	if match := duanju51DigitPattern.FindString(remark); match != "" && strings.Contains(remark, "集") {
		episodes = match
	}
	views := ""
	if heat := duanju51VisibleText(duanju51ClassFind(card, "hg-rank-item__heat-value")); heat != "" {
		views = normalizeViews(heat)
	}

	var tags []string
	for _, node := range maccmsNodes(card, func(current *xhtml.Node) bool {
		return maccmsHasClass(current, "hg-tag")
	}) {
		if tag := duanju51VisibleText(node); tag != "" {
			tags = append(tags, tag)
		}
	}

	return Drama{
		ID:           providerDramaID(sourceDuanju51, videoID),
		Source:       sourceDuanju51,
		SourceID:     videoID,
		Title:        title,
		Name:         title,
		Desc:         duanju51VisibleText(duanju51ClassFind(card, "hg-drama-card__desc", "hg-rank-item__desc")),
		Intro:        duanju51VisibleText(duanju51ClassFind(card, "hg-drama-card__desc", "hg-rank-item__desc")),
		Cover:        cover,
		CoverURL:     cover,
		ChannelName:  "51短剧",
		CategoryName: firstNonEmpty(maccmsAttr(card, "data-track-type-name"), "短剧"),
		Remark:       remark,
		TotalEpisode: episodes,
		EpisodeCount: episodes,
		Tags:         tags,
		Views:        views,
	}, true
}

func duanju51ClassFind(root *xhtml.Node, names ...string) *xhtml.Node {
	return maccmsFind(root, func(node *xhtml.Node) bool {
		for _, name := range names {
			if maccmsHasClass(node, name) {
				return true
			}
		}
		return false
	})
}

// duanju51VisibleText 与 maccmsText 类似，但额外丢掉 sr-only 辅助文本
// （卡片标题里塞了「全集在线观看」，混进来会污染剧名）。
func duanju51VisibleText(node *xhtml.Node) string {
	if node == nil {
		return ""
	}
	var builder strings.Builder
	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current.Type == xhtml.TextNode {
			builder.WriteString(current.Data)
			builder.WriteByte(' ')
			return
		}
		if current.Type == xhtml.ElementNode {
			switch strings.ToLower(current.Data) {
			case "script", "style", "noscript", "form":
				return
			}
			if maccmsHasClass(current, "sr-only") {
				return
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(strings.Fields(builder.String()), " ")
}

func duanju51AbsoluteURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	if strings.HasPrefix(raw, "/") {
		return duanju51BaseURL + raw
	}
	return raw
}

// ---------------------------------------------------------------------------
// devalue 扁平数组解码器
//
// 51 自身已经不用 Nuxt，但野果（provider_yeguo.go 的 fetchYeguoPayload）
// 仍复用这份解码器读 _payload.json，所以必须保留，不能随 51 改版一起删掉。
// ---------------------------------------------------------------------------

var duanju51DevalueSpecial = map[string]bool{
	"ShallowReactive": true, "Reactive": true, "Ref": true, "ShallowRef": true,
	"ToRaw": true, "Readonly": true, "ShallowReadonly": true,
	"Set": true, "Map": true, "Date": true, "RegExp": true,
}

// duanju51DecodePayload 还原 devalue 的扁平数组。
//
// 序列化格式：整个 payload 是一个数组，数组下标即「槽位」。
// 槽位里的整数代表另一个槽位的下标，字符串/布尔/浮点等是字面量。
// 形如 ["Ref", 12] 的二元组表示「取 12 号槽位的值」。
func duanju51DecodePayload(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var slots []any
	if err := json.Unmarshal([]byte(raw), &slots); err != nil {
		return nil
	}
	if len(slots) == 0 {
		return nil
	}

	memo := make(map[int]any, len(slots))
	done := make(map[int]bool, len(slots))
	active := make(map[int]bool, 8)

	var resolve func(int) any
	var read func(any) any

	read = func(value any) any {
		switch typed := value.(type) {
		case []any:
			out := make([]any, len(typed))
			for i, item := range typed {
				out[i] = read(item)
			}
			return out
		case map[string]any:
			out := make(map[string]any, len(typed))
			for key, item := range typed {
				out[key] = read(item)
			}
			return out
		case float64:
			if typed == float64(int64(typed)) {
				return resolve(int(typed))
			}
			return typed
		default:
			return value
		}
	}

	resolve = func(index int) any {
		if index < 0 || index >= len(slots) {
			return index
		}
		if done[index] {
			return memo[index]
		}
		if active[index] {
			// 循环引用：返回 nil，避免无限递归。
			return nil
		}
		active[index] = true
		value := slots[index]
		var result any
		if list, ok := value.([]any); ok {
			switch {
			case len(list) == 2 && duanju51DevalueSpecial[duanju51DevalueMarker(list[0])]:
				if target, ok := list[1].(float64); ok {
					result = resolve(int(target))
				} else {
					result = read(list[1])
				}
			case len(list) >= 3 && duanju51DevalueMarker(list[0]) == "Set":
				items := make([]any, 0, len(list)-1)
				for _, item := range list[1:] {
					items = append(items, read(item))
				}
				result = items
			case len(list) >= 3 && duanju51DevalueMarker(list[0]) == "Map":
				pairs := make(map[string]any, (len(list)-1)/2)
				for i := 1; i+1 < len(list); i += 2 {
					pairs[fmt.Sprint(read(list[i]))] = read(list[i+1])
				}
				result = pairs
			default:
				result = read(list)
			}
		} else if object, ok := value.(map[string]any); ok {
			converted := make(map[string]any, len(object))
			for key, item := range object {
				converted[key] = read(item)
			}
			result = converted
		} else {
			result = value
		}
		delete(active, index)
		memo[index] = result
		done[index] = true
		return result
	}

	return resolve(0)
}

func duanju51DevalueMarker(value any) string {
	text, _ := value.(string)
	return text
}
