package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// 51短剧在 2026-11 从 Nuxt 3 / __NUXT_DATA__ 改成了服务端渲染的 hg-* 模板。
// 这一组用例用最小化的真实结构夹具锁住：卡片 / 榜单 DOM、分集按钮、
// videoInitialData JSON、以及「按集页面换新鲜 auth_key」的取流链路。
// 站点本身是否可达由 DUANJU51_LIVE=1 的冒烟用例和 CI 巡检覆盖。

// duanju51DecodePayload 仍被野果（provider_yeguo.go）用来读 _payload.json，
// 这里保留解引用逻辑的最小回归，避免改 51 时把邻居一起改坏。
func TestDuanju51DecodePayloadResolvesReferences(t *testing.T) {
	payload := `[
		{"list": 1, "total": 2},
		[2, 3],
		{"video_id": 13679, "title": "剧集甲", "cover": "https://pic.example/a.jpg"},
		{"video_id": 13012, "title": "剧集乙"}
	]`
	root := duanju51DecodePayload(payload)
	object, ok := root.(map[string]any)
	if !ok {
		t.Fatalf("payload 根节点应是对象，实际 %T", root)
	}
	list, ok := object["list"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("引用数组没有还原: %#v", object["list"])
	}
	first, ok := list[0].(map[string]any)
	if !ok || fmt.Sprint(first["title"]) != "剧集甲" {
		t.Fatalf("首个卡片字段不对: %#v", list[0])
	}
	// devalue 里整数就是槽位下标：{"total": 2} 会解引用到第 2 号槽位
	// （也就是第一张卡片），这正是野果 _payload.json 依赖的语义。
	if referenced, ok := object["total"].(map[string]any); !ok || fmt.Sprint(referenced["title"]) != "剧集甲" {
		t.Fatalf("整数槽位引用未还原: %#v", object["total"])
	}
}

// 列表页夹具：hidden 的「猜你喜欢」与 <template> 里的卡片都必须被排除，
// 主列表用 img[data-src]（src 是占位图），集数写在 __episode。
const duanju51CatalogFixture = `<!doctype html><html><body>
<div class="hg-search-suggest"><div class="hg-search-suggest__guess">
  <div class="hg-drama-card"><a href="/video/99999/"><img data-src="https://pic.example/guess.jpg"></a><div class="hg-drama-card__title">猜你喜欢混入</div></div>
</div></div>
<template><div class="hg-drama-card"><a href="/video/88888/"><img data-src="https://pic.example/tpl.jpg"></a><div class="hg-drama-card__title">模板卡片混入</div></div></template>
<main><div class="hg-card-grid">
  <div class="hg-drama-card">
    <div class="hg-drama-card__cover"><a class="hg-drama-card__cover-link" href="/video/14049/"><img src="/static/web/images/cover-placeholder-51.svg" data-src="https://pic.example/14049.jpeg" alt="爱情公寓"><span class="sr-only">《爱情公寓》AI短剧题材</span></a><span class="hg-drama-card__episode">全5集</span></div>
    <div class="hg-drama-card__body"><div class="hg-drama-card__title"><a href="/video/14049/">爱情公寓<span class="sr-only">全集在线观看</span></a></div><p class="hg-drama-card__desc">四个人同时动了心。</p><div class="hg-drama-card__tags"><a class="hg-tag">AI短剧<span class="sr-only">题材短剧</span></a></div></div>
  </div>
  <div class="hg-drama-card" data-track-id="13012" data-track-title="剧集乙" data-track-type-name="AI漫剧">
    <div class="hg-drama-card__cover"><a href="/video/13012/"><img data-src="https://pic.example/13012.jpeg"></a><span class="hg-drama-card__episode">全12集</span></div>
    <div class="hg-drama-card__body"><div class="hg-drama-card__title">被覆盖的标题</div></div>
  </div>
</div>
<nav class="hg-pager"><a class="hg-pager__page" href="/newest/2/">2</a></nav>
</main></body></html>`

func TestDuanju51CardDramasParsesSSRCards(t *testing.T) {
	cards := duanju51CardDramas(duanju51CatalogFixture)
	if len(cards) != 2 {
		t.Fatalf("应只保留主列表 2 部剧，实际 %d: %#v", len(cards), cards)
	}
	first := cards[0]
	if first.ID != "51duanju:14049" || first.Source != sourceDuanju51 || first.SourceID != "14049" {
		t.Errorf("ID 映射错误: %+v", first)
	}
	if first.Title != "爱情公寓" || first.Name != "爱情公寓" {
		t.Errorf("sr-only 文本污染了剧名: %q", first.Title)
	}
	if first.Cover != "https://pic.example/14049.jpeg" {
		t.Errorf("封面应取 data-src，实际 %v", first.Cover)
	}
	if first.Remark != "全5集" || first.TotalEpisode != "5" || first.EpisodeCount != "5" {
		t.Errorf("集数解析错误: remark=%q total=%v count=%v", first.Remark, first.TotalEpisode, first.EpisodeCount)
	}
	if first.Desc != "四个人同时动了心。" || len(first.Tags) != 1 || first.Tags[0] != "AI短剧" {
		t.Errorf("简介 / 标签解析错误: %+v", first)
	}
	if first.ChannelName != "51短剧" || first.CategoryName != "短剧" {
		t.Errorf("频道 / 分类错误: %+v", first)
	}
	second := cards[1]
	if second.SourceID != "13012" || second.Title != "剧集乙" {
		t.Errorf("data-track-* 优先于子节点: %+v", second)
	}
	if second.CategoryName != "AI漫剧" {
		t.Errorf("data-track-type-name 未生效: %q", second.CategoryName)
	}
	if !duanju51HasNextPage(duanju51CatalogFixture, 1) {
		t.Error("第 1 页应识别到 /newest/2/ 下一页")
	}
	if duanju51HasNextPage(duanju51CatalogFixture, 2) {
		t.Error("页码 2 之后不应还有下一页")
	}
}

const duanju51RankFixture = `<!doctype html><html><body><main><div class="hg-rank-list" data-rank-list>
<div class="hg-rank-item" data-rank-item data-rank="1" data-track-id="12943" data-track-title="秘密教学" data-track-type-name="AI成人短剧">
  <a href="/video/12943/" class="hg-rank-item__cover"><img src="/static/web/images/cover-placeholder-51.svg" data-src="https://pic.example/12943.jpeg"></a>
  <h2 class="hg-rank-item__title"><a href="/video/12943/">秘密教学</a></h2>
  <p class="hg-rank-item__desc">简介甲</p>
  <div class="hg-rank-item__heat"><span class="hg-rank-item__heat-value">12.3万</span></div>
</div>
<div class="hg-rank-item" data-rank-item data-rank="2" data-track-id="13013" data-track-title="艳母" data-track-type-name="AI成人短剧">
  <a href="/video/13013/" class="hg-rank-item__cover"><img data-src="https://pic.example/13013.jpeg"></a>
  <h2 class="hg-rank-item__title"><a href="/video/13013/">艳母</a></h2>
  <p class="hg-rank-item__desc">简介乙</p>
</div>
</div></main></body></html>`

func TestDuanju51CardDramasParsesRankItems(t *testing.T) {
	cards := duanju51CardDramas(duanju51RankFixture)
	if len(cards) != 2 {
		t.Fatalf("榜单应解析出 2 条，实际 %d", len(cards))
	}
	if cards[0].ID != "51duanju:12943" || cards[0].Title != "秘密教学" {
		t.Errorf("榜单首条字段错误: %+v", cards[0])
	}
	if cards[0].Cover != "https://pic.example/12943.jpeg" {
		t.Errorf("榜单封面应取 data-src，实际 %v", cards[0].Cover)
	}
	if cards[0].Remark != "热度 12.3万" {
		t.Errorf("热力值未转成备注: %q", cards[0].Remark)
	}
	if cards[0].Views != "12.3万次播放" {
		t.Errorf("热力值未转成播放量: %v", cards[0].Views)
	}
	if cards[1].CategoryName != "AI成人短剧" {
		t.Errorf("榜单分类丢失: %q", cards[1].CategoryName)
	}
	if duanju51HasNextPage(duanju51RankFixture, 1) {
		t.Error("榜单是单页，不应有下一页")
	}
}

// duanju51DetailHTML 按真实页面结构生成详情页：分集按钮给全量集号，
// <script id="videoInitialData"> 是普通 JSON，epPlaySrcs 只是当前集附近的窗口。
func duanju51DetailHTML(t *testing.T, ep int, window map[string]string, videoSrc, source string) string {
	t.Helper()
	data := map[string]any{
		"id": "14194", "title": "沉默的羔羊", "description": "密闭空间里的伪装被一步步剥开。",
		"coverSrc": "/upload/cover/14194.jpeg", "ep": ep, "epPlaySrcs": window,
		"episodeDurationLabels": map[string]string{"1": "", "2": "", "3": ""},
		"trackingEpisodes":      map[string]any{"1": map[string]any{"episode": 1}},
		"videoSrc":              videoSrc, "views": "8.8万",
		"breadcrumb": []any{map[string]any{"name": "AI成人短剧", "url": "/ai-duanju/"}},
		"tags":       []any{"悬疑", "都市"},
	}
	payload, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`<!doctype html><html><body><main>
<div class="hg-web-play__episode-scroll" data-web-play-episode-scroll><div class="hg-web-play__ep-grid">
<a class="hg-web-play__ep is-active" href="/video/14194/" data-ep-id="1">01</a>
<a class="hg-web-play__ep" href="/video/14194/ep-2/" data-ep-id="2">02</a>
<a class="hg-web-play__ep" href="/video/14194/ep-3/" data-ep-id="3">03</a>
</div></div>
<video class="hg-web-play__video"><source src="%s" type="application/x-mpegURL"></video>
<script id="videoInitialData" type="application/json">%s</script>
</main></body></html>`, source, string(payload))
}

func TestDuanju51InitialDataAndEpisodeIndexes(t *testing.T) {
	address := "https://hls.example/ep1.m3u8?auth_key=abc&v=3"
	body := duanju51DetailHTML(t, 1, map[string]string{"1": address, "2": "https://hls.example/ep2.m3u8"}, address, "")
	doc := maccmsDocument(body)
	data := duanju51InitialData(doc)
	if data == nil {
		t.Fatal("videoInitialData 未解析出来")
	}
	if mapString(data, "title") != "沉默的羔羊" || duanju51BreadcrumbCategory(data) != "AI成人短剧" {
		t.Errorf("详情元信息解析错误: %v / %q", mapString(data, "title"), duanju51BreadcrumbCategory(data))
	}
	if got := duanju51EpisodeIndexes(doc, data); len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Errorf("分集按钮集号错误: %v", got)
	}
	if got := duanju51EpisodeMedia(doc, data, 1); got != address {
		t.Errorf("第 1 集应优先用 epPlaySrcs[1]，实际 %q", got)
	}
	if got := duanju51EpisodeMedia(doc, data, 3); got != address {
		t.Errorf("第 3 集不在窗口内时应回退 videoSrc，实际 %q", got)
	}
	// JSON 里 & 被 Go 编码成 \u0026，解析回来必须是可用的 &。
	if !strings.Contains(address, "&v=3") || !strings.Contains(duanju51EpisodeMedia(doc, data, 1), "&v=3") {
		t.Error("auth_key 查询串被破坏")
	}
	// <source> 兜底：JSON 里既没窗口也没 videoSrc 时仍能取到地址。
	fallback := duanju51DetailHTML(t, 1, map[string]string{}, "", "https://hls.example/fallback.m3u8")
	fallbackDoc := maccmsDocument(fallback)
	if got := duanju51EpisodeMedia(fallbackDoc, duanju51InitialData(fallbackDoc), 1); got != "https://hls.example/fallback.m3u8" {
		t.Errorf("<source> 兜底失效: %q", got)
	}
}

// duanju51FixtureDownloader 把镜像缓存固定成空，避免其它用例的残留域名
// 把请求形状变成多候选竞速（也会顺带触发远程配置拉取）。
func duanju51FixtureDownloader(t *testing.T, handler func(*http.Request) (*http.Response, error)) *Downloader {
	t.Helper()
	withProviderMirrorCacheForTest(t, nil)
	return sourceFixtureDownloader(t, sourceFixtureTransport(handler))
}

func TestDuanju51DetailBuildsPlayPlaceholders(t *testing.T) {
	const first = "https://hls.example/ep1.m3u8?auth_key=aaa&v=3"
	var seen []string
	d := duanju51FixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		seen = append(seen, request.URL.Path)
		return sourceFixtureResponse(request, http.StatusOK,
			duanju51DetailHTML(t, 1, map[string]string{"1": first, "2": "https://hls.example/ep2.m3u8"}, first, "")), nil
	})
	drama, chapters, err := d.fetchDuanju51Detail(context.Background(), "14194")
	if err != nil {
		t.Fatal(err)
	}
	if drama.ID != "51duanju:14194" || drama.Title != "沉默的羔羊" || drama.TotalEpisode != "3" {
		t.Errorf("详情元信息错误: %+v", drama)
	}
	if drama.Cover != "https://blue.xdrgcewe.cc/upload/cover/14194.jpeg" {
		t.Errorf("相对封面未补成绝对地址: %v", drama.Cover)
	}
	if drama.CategoryName != "AI成人短剧" || len(drama.Tags) != 2 {
		t.Errorf("分类 / 标签错误: %+v", drama)
	}
	if drama.Views != "8.8万次播放" {
		t.Errorf("播放量错误: %v", drama.Views)
	}
	if len(chapters) != 3 {
		t.Fatalf("应有 3 集，实际 %d", len(chapters))
	}
	for i, chapter := range chapters {
		episode := i + 1
		want := duanju51PlayPlaceholder("14194", episode)
		if chapter.VideoURL != want {
			t.Errorf("第 %d 集应存占位地址 %q，实际 %q", episode, want, chapter.VideoURL)
		}
		if !strings.Contains(chapter.PageURL, "/video/14194/") || chapter.Referer != chapter.PageURL {
			t.Errorf("第 %d 集页面地址错误: %q / %q", episode, chapter.PageURL, chapter.Referer)
		}
	}
	if len(seen) != 1 || seen[0] != "/video/14194/" {
		t.Errorf("详情只应请求第 1 集页面一次: %v", seen)
	}
}

func TestDuanju51ResolvePlaybackFetchesEpisodePage(t *testing.T) {
	const third = "https://hls.example/ep3.m3u8?auth_key=ccc&v=3"
	var seen []string
	d := duanju51FixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		seen = append(seen, request.URL.Path+"?"+request.URL.RawQuery)
		switch {
		case request.URL.Path == "/video/14194/ep-3/":
			window := map[string]string{"2": "https://hls.example/ep2.m3u8", "3": third, "4": "https://hls.example/ep4.m3u8"}
			return sourceFixtureResponse(request, http.StatusOK, duanju51DetailHTML(t, 3, window, third, "")), nil
		case strings.Contains(request.URL.Path, ".m3u8"):
			return sourceFixtureResponse(request, http.StatusOK, "#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:4,\nsegment.ts\n#EXT-X-ENDLIST\n"), nil
		default:
			return sourceFixtureResponse(request, http.StatusNotFound, "missing"), nil
		}
	})
	chapter := Chapter{
		ID:             providerChapterID(sourceDuanju51, "14194", "3"),
		Source:         sourceDuanju51,
		Title:          "第3集",
		VideoURL:       duanju51PlayPlaceholder("14194", 3),
		CurrentEpisode: rawEpisode(3),
	}
	media, err := d.resolveProviderMedia(context.Background(), Task{
		DramaID: "51duanju:14194", Chapter: chapter, Index: 3, Total: 3,
	})
	if err != nil {
		t.Fatalf("播放解析失败: %v", err)
	}
	if media.URL != third {
		t.Errorf("应换出第 3 集的新地址，实际 %q", media.URL)
	}
	if !strings.HasPrefix(media.Playlist, "#EXTM3U") {
		t.Errorf("播放列表无效: %q", media.Playlist)
	}
	if !strings.Contains(media.Referer, "/video/14194/ep-3/") {
		t.Errorf("Referer 应指向当前集页面，实际 %q", media.Referer)
	}
	if len(seen) != 2 || !strings.HasPrefix(seen[0], "/video/14194/ep-3/?") || !strings.Contains(seen[1], "ep3.m3u8") {
		t.Errorf("请求链应为「当前集页面 → m3u8」，实际 %v", seen)
	}
}

// 老版本保存下来的裸 m3u8 也带 auth_key，会过期；必须走同一条按需解析，
// 用 DramaID + 当前集号重新抓页面，而不是直接拿旧地址去拉播放列表。
func TestDuanju51LegacyAuthKeyURLIsRefreshed(t *testing.T) {
	const second = "https://hls.example/ep2.m3u8?auth_key=fresh&v=3"
	var seen []string
	d := duanju51FixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		seen = append(seen, request.URL.String())
		switch {
		case request.URL.Path == "/video/14194/ep-2/":
			return sourceFixtureResponse(request, http.StatusOK,
				duanju51DetailHTML(t, 2, map[string]string{"2": second}, second, "")), nil
		case strings.Contains(request.URL.Path, ".m3u8"):
			return sourceFixtureResponse(request, http.StatusOK, "#EXTM3U\n#EXTINF:4,\nsegment.ts\n#EXT-X-ENDLIST\n"), nil
		default:
			return sourceFixtureResponse(request, http.StatusNotFound, "missing"), nil
		}
	})
	legacy := "https://hls.example/old.m3u8?auth_key=expired"
	media, err := d.resolveProviderMedia(context.Background(), Task{
		DramaID: "51duanju:14194",
		Chapter: Chapter{Source: sourceDuanju51, Title: "第2集", VideoURL: legacy, CurrentEpisode: rawEpisode(2)},
		Index:   2, Total: 3,
	})
	if err != nil {
		t.Fatalf("历史链接刷新失败: %v", err)
	}
	if media.URL != second {
		t.Errorf("应换成新鲜地址，实际 %q", media.URL)
	}
	if strings.Contains(strings.Join(seen, " "), "old.m3u8") {
		t.Errorf("不应再请求过期的旧地址: %v", seen)
	}
}

func TestDuanju51RoutesAndPlaceholders(t *testing.T) {
	catalog := map[string]string{
		"home":     "/",
		"rank":     "/ranks/hot/",
		"theater":  "/ai-duanju/",
		"new":      "/newest/",
		"manju":    "/ai-manju/",
		"huanlian": "/ai-huanlian/",
		"mogai":    "/ai-mogai/",
	}
	for category, want := range catalog {
		if got := duanju51CatalogPath(category, 1); got != want {
			t.Errorf("%s 第 1 页路径 = %q，want %q", category, got, want)
		}
	}
	if got := duanju51CatalogPath("theater", 3); got != "/ai-duanju/3/" {
		t.Errorf("分类分页路径错误: %q", got)
	}
	if got := duanju51CatalogPath("home", 2); got != "" {
		t.Errorf("首页不应有第 2 页: %q", got)
	}
	if got := duanju51CatalogPath("bogus", 2); got != "/ai-duanju/2/" {
		t.Errorf("未知分类应回退到 AI 短剧: %q", got)
	}
	search := duanju51SearchPath("沉默的羔羊", 1)
	if !strings.HasPrefix(search, "/search/?keyword=") || strings.Contains(search, "沉默") {
		t.Errorf("搜索关键词应 URL 编码: %q", search)
	}
	if !strings.Contains(duanju51SearchPath("沉默", 2), "page=2") {
		t.Error("搜索第 2 页应带 page 参数")
	}
	if duanju51SearchPath("  ", 1) != "" {
		t.Error("空搜索不应生成路径")
	}
	if got := duanju51DetailPath("14194", 1); got != "/video/14194/" {
		t.Errorf("第 1 集详情页路径错误: %q", got)
	}
	if got := duanju51DetailPath("14194", 9); got != "/video/14194/ep-9/" {
		t.Errorf("第 9 集详情页路径错误: %q", got)
	}
	for _, raw := range []string{"51duanju-play://14194/3", "51duanju-play://14194/3/"} {
		id, episode, ok := duanju51ParsePlayPlaceholder(raw)
		if !ok || id != "14194" || episode != 3 {
			t.Errorf("占位地址解析错误 %q -> %q/%d/%v", raw, id, episode, ok)
		}
	}
	for _, raw := range []string{"", "https://hls.example/a.m3u8", "51duanju-play://abc/2", "51duanju-play://14194/0"} {
		if _, _, ok := duanju51ParsePlayPlaceholder(raw); ok {
			t.Errorf("非法占位地址不应通过: %q", raw)
		}
	}
}

func TestDuanju51RankingUsesSinglePageRoutes(t *testing.T) {
	routes := map[string]string{"total": "/ranks/hot/", "week": "/ranks/recommend/", "month": "/ranks/potential/"}
	for key, want := range routes {
		var got string
		d := duanju51FixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
			got = request.URL.Path
			return sourceFixtureResponse(request, http.StatusOK, duanju51RankFixture), nil
		})
		page, err := d.fetchRankingPage(context.Background(), rankingBoard{
			ID: "51duanju-" + key, Source: sourceDuanju51, Name: "测试榜", upstreamKey: key,
		}, 1)
		if err != nil {
			t.Fatalf("%s 榜单解析失败: %v", key, err)
		}
		if got != want {
			t.Errorf("%s 应请求 %q，实际 %q", key, want, got)
		}
		if len(page.Items) != 2 || page.HasMore || page.TotalPages != 1 {
			t.Errorf("%s 榜单应是单页 2 条: %+v", key, page)
		}
		if page.Items[0].Rank != 1 || page.Items[1].Rank != 2 {
			t.Errorf("%s 榜位错误: %+v", key, page.Items)
		}
	}
	// 单页榜请求第 2 页必须直接报错，不能再去打一次 404。
	d := duanju51FixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		t.Error("第 2 页不应发起请求")
		return sourceFixtureResponse(request, http.StatusNotFound, "missing"), nil
	})
	if _, err := d.fetchRankingPage(context.Background(), rankingBoard{
		ID: "51duanju-total", Source: sourceDuanju51, upstreamKey: "total",
	}, 2); err == nil {
		t.Error("单页榜第 2 页应被拒绝")
	}
}

// 真实站点冒烟测试，默认跳过；需要时用 DUANJU51_LIVE=1 打开。
func TestDuanju51LiveCatalogAndDetail(t *testing.T) {
	if os.Getenv("DUANJU51_LIVE") == "" {
		t.Skip("set DUANJU51_LIVE=1 to hit the live site")
	}
	d := &Downloader{
		cfg:           Config{Retries: 2, PageSize: 30},
		client:        &http.Client{Timeout: 30 * time.Second},
		limiter:       newRequestLimiter(3, 0),
		providerHosts: map[string]string{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	items, _, err := d.fetchDuanju51CatalogPage(ctx, 1, "home", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("首页没有解析出任何剧集")
	}
	t.Logf("首页 %d 部，首部=%s (%s)", len(items), items[0].Title, items[0].SourceID)

	drama, chapters, err := d.fetchDuanju51Detail(ctx, items[0].SourceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) == 0 {
		t.Fatal("详情没有分集")
	}
	if !strings.HasPrefix(chapters[0].VideoURL, duanju51PlayScheme) {
		t.Fatalf("首集应存按需解析占位，实际 %q", chapters[0].VideoURL)
	}
	t.Logf("详情 %s，%d 集", drama.Title, len(chapters))

	media, err := d.resolveProviderMedia(ctx, Task{DramaID: drama.ID, Chapter: chapters[0], Index: 1, Total: len(chapters)})
	if err != nil {
		t.Fatalf("播放解析失败: %v", err)
	}
	if !strings.Contains(media.URL, "auth_key=") {
		t.Errorf("换出的地址应带时效签名: %q", media.URL)
	}
	if !strings.HasPrefix(strings.TrimSpace(media.Playlist), "#EXTM3U") {
		t.Fatalf("播放列表无效: %q", media.Playlist[:min(40, len(media.Playlist))])
	}
	t.Logf("播放列表 %d 字节", len(media.Playlist))
}
