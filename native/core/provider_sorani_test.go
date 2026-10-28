package core

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// 青空次元（sorani）站源单测。
//
// 上游在本机沙箱里不可达（`api.sorani.cc` 直接连不上），所以全部用
// fixture 覆盖：请求形状、字段映射、错误码、占位地址与真实地址的换取。
// 站点本身是否还活着只能靠真机验证，这里只保证"我们的解析逻辑对"。

const soraniCatalogFixture = `{"code":200,"message":"ok","data":{"total":30,"records":[
 {"id":101,"title":"夏日重现","summary":"离岛悬疑。","cover":"/upload/cover/101.jpg",
  "episodeCount":25,"categoryName":"TV番剧","area":"日本","year":"2022-04-15",
  "bangumiRating":"8.6","tags":["悬疑","奇幻"]},
 {"id":102,"title":"剧场版 某作","summary":"","coverLarge":"https://cdn.example.test/102.jpg",
  "total":1,"categoryName":"剧场动画"}
]}}`

const soraniDetailFixture = `{"code":200,"data":{"id":101,"title":"夏日重现","summary":"离岛悬疑。",
 "cover":"/upload/cover/101.jpg","episodeCount":2,"categoryName":"TV番剧",
 "episodes":[
  {"episodeId":9001,"episodeOrder":1,"episodeLabel":"第 1 集","enabled":true},
  {"episodeId":9002,"episodeOrder":2,"episodeLabel":"第 2 集","enabled":false},
  {"episodeId":9003,"episodeOrder":2,"episodeLabel":"第 2 集","enabled":true}
 ]}}`

func soraniFixtureDownloader(t *testing.T, handler func(*http.Request) (*http.Response, error)) *Downloader {
	t.Helper()
	return sourceFixtureDownloader(t, sourceFixtureTransport(handler))
}

func TestSoraniCatalogParsesRecords(t *testing.T) {
	var seen []string
	downloader := soraniFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		seen = append(seen, request.URL.Path+"?"+request.URL.RawQuery)
		if got := request.Header.Get("Referer"); !strings.HasPrefix(got, "https://") {
			t.Errorf("Referer 应为站点根地址，实际 %q", got)
		}
		return sourceFixtureResponse(request, http.StatusOK, soraniCatalogFixture), nil
	})

	items, more, err := downloader.fetchSoraniCatalogPage(context.Background(), 1, "", "")
	if err != nil {
		t.Fatalf("目录解析失败: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("应解析出 2 部剧，实际 %d", len(items))
	}
	first := items[0]
	if first.ID != "sorani:101" || first.Source != sourceSorani || first.SourceID != "101" {
		t.Errorf("ID 映射错误: %+v", first)
	}
	if first.Title != "夏日重现" || first.Desc != "离岛悬疑。" {
		t.Errorf("标题 / 简介映射错误: %+v", first)
	}
	if !strings.HasPrefix(nativeText(first.Cover), "https://") {
		t.Errorf("封面应补全成绝对地址，实际 %v", first.Cover)
	}
	if nativeText(first.EpisodeCount) != "25" {
		t.Errorf("集数应为 25，实际 %v", first.EpisodeCount)
	}
	if first.ChannelName != "青空次元" || first.Category != "TV番剧" {
		t.Errorf("频道 / 分类错误: %+v", first)
	}
	if !more {
		t.Errorf("total=30 > 24，应还有下一页")
	}
	if len(seen) != 1 || !strings.Contains(seen[0], "/video?") || !strings.Contains(seen[0], "size=24") {
		t.Errorf("目录请求形状不对: %v", seen)
	}
}

func TestSoraniSearchUsesSearchRoute(t *testing.T) {
	var path string
	downloader := soraniFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		path = request.URL.Path + "?" + request.URL.RawQuery
		return sourceFixtureResponse(request, http.StatusOK, `{"code":200,"data":{"records":[]}}`), nil
	})

	items, more, err := downloader.fetchSoraniCatalogPage(context.Background(), 1, "", "夏日")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(items) != 0 || more {
		t.Errorf("空结果应返回 0 部且没有下一页，实际 %d / %v", len(items), more)
	}
	if !strings.Contains(path, "/video/search") || !strings.Contains(path, "keyword=") {
		t.Errorf("搜索应走 /video/search 并带 keyword，实际 %s", path)
	}
}

func TestSoraniCatalogRejectsBusinessError(t *testing.T) {
	downloader := soraniFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusOK, `{"code":500,"message":"服务维护中"}`), nil
	})
	_, _, err := downloader.fetchSoraniCatalogPage(context.Background(), 1, "", "")
	if err == nil || !strings.Contains(err.Error(), "服务维护中") {
		t.Fatalf("应把上游业务错误原样带出来，实际 %v", err)
	}
}

func TestSoraniCatalogRejectsUnknownCategory(t *testing.T) {
	downloader := soraniFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		t.Errorf("分类非法时不应发起请求")
		return sourceFixtureResponse(request, http.StatusOK, "{}"), nil
	})
	if _, _, err := downloader.fetchSoraniCatalogPage(context.Background(), 1, "9", ""); err == nil {
		t.Fatal("未知分类应直接拒绝")
	}
}

func TestSoraniDetailBuildsPlayableChapters(t *testing.T) {
	downloader := soraniFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(request.URL.Path, "/video/101") {
			t.Errorf("详情应请求 /video/101，实际 %s", request.URL.Path)
		}
		return sourceFixtureResponse(request, http.StatusOK, soraniDetailFixture), nil
	})

	drama, chapters, err := downloader.fetchSoraniDetail(context.Background(), "101")
	if err != nil {
		t.Fatalf("详情解析失败: %v", err)
	}
	if drama.ID != "sorani:101" || drama.Title != "夏日重现" {
		t.Errorf("详情元信息错误: %+v", drama)
	}
	// enabled=false 的那一集要被跳过，剩下两集。
	if len(chapters) != 2 {
		t.Fatalf("应保留 2 集，实际 %d", len(chapters))
	}
	if chapters[0].ID != "sorani:101:9001" || chapters[1].ID != "sorani:101:9003" {
		t.Errorf("章节 ID 错误: %s / %s", chapters[0].ID, chapters[1].ID)
	}
	for _, chapter := range chapters {
		if !strings.HasPrefix(chapter.VideoURL, "sorani-play://") {
			t.Errorf("分集应存占位标记，实际 %s", chapter.VideoURL)
		}
	}
	if chapters[0].EpisodeString(9) != "1" || chapters[1].EpisodeString(9) != "2" {
		t.Errorf("集号应来自 episodeOrder: %s / %s",
			chapters[0].EpisodeString(9), chapters[1].EpisodeString(9))
	}
}

func TestSoraniDetailRejectsDuplicateEpisodeOrder(t *testing.T) {
	const body = `{"code":200,"data":{"id":101,"title":"夏日重现","episodes":[
	 {"episodeId":1,"episodeOrder":1},{"episodeId":2,"episodeOrder":1}]}}`
	downloader := soraniFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusOK, body), nil
	})
	if _, _, err := downloader.fetchSoraniDetail(context.Background(), "101"); err == nil {
		t.Fatal("集号重复应报错，否则选集顺序会乱")
	}
}

func TestSoraniResolveMediaReturnsPlayAddress(t *testing.T) {
	downloader := soraniFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(request.URL.Path, "/video/episode/9001/play") {
			t.Errorf("应请求 play 接口，实际 %s", request.URL.Path)
		}
		if got := request.URL.Query().Get("lineCode"); got != soraniLineCodeDefault {
			t.Errorf("线路应为 %s，实际 %q", soraniLineCodeDefault, got)
		}
		return sourceFixtureResponse(request, http.StatusOK,
			`{"code":200,"data":{"playUrl":"https://cdn.example.test/9001/index.m3u8"}}`), nil
	})

	task := Task{
		DramaID: "sorani:101",
		Chapter: Chapter{ID: "sorani:101:9001", Source: sourceSorani,
			VideoURL: "sorani-play://9001"},
	}
	media, err := downloader.resolveSoraniMedia(context.Background(), task)
	if err != nil {
		t.Fatalf("换取播放地址失败: %v", err)
	}
	if media.URL != "https://cdn.example.test/9001/index.m3u8" {
		t.Errorf("播放地址错误: %s", media.URL)
	}
	if !strings.HasPrefix(media.Referer, "https://") {
		t.Errorf("Referer 应指向站点根，实际 %q", media.Referer)
	}
}

func TestSoraniResolveMediaReusesPlayAddress(t *testing.T) {
	// 实测：同一个 episodeId 连着请求两次 play 接口，第二次返回空 playUrl。
	// 我们的链路里同一集被解析两次是常态（预载回退 / 解析后预缓存 / 连点切集），
	// 所以换出来的地址必须复用。
	var calls int32
	downloader := soraniFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return sourceFixtureResponse(request, http.StatusOK,
			`{"code":200,"data":{"playUrl":"https://cdn.example.test/9001/index.m3u8"}}`), nil
	})
	task := Task{
		DramaID: "sorani:101",
		Chapter: Chapter{ID: "sorani:101:9001", Source: sourceSorani,
			VideoURL: "sorani-play://9001"},
	}
	for i := 0; i < 2; i++ {
		media, err := downloader.resolveSoraniMedia(context.Background(), task)
		if err != nil {
			t.Fatalf("第 %d 次换取失败: %v", i+1, err)
		}
		if media.URL != "https://cdn.example.test/9001/index.m3u8" {
			t.Errorf("第 %d 次地址错误: %s", i+1, media.URL)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("同一集应复用已换出的地址，实际请求了 %d 次", got)
	}
}

func TestSoraniResolveMediaRejectsForeignChapter(t *testing.T) {
	downloader := soraniFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		t.Errorf("章节不属于本剧时不应发起请求")
		return sourceFixtureResponse(request, http.StatusOK, "{}"), nil
	})
	task := Task{
		DramaID: "sorani:101",
		Chapter: Chapter{ID: "sorani:202:9001", Source: sourceSorani,
			VideoURL: "sorani-play://9001"},
	}
	if _, err := downloader.resolveSoraniMedia(context.Background(), task); err == nil {
		t.Fatal("章节 ID 与本剧不匹配应拒绝")
	}
}

func TestSoraniResolveMediaReportsEmptyPlayURL(t *testing.T) {
	downloader := soraniFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusOK, `{"code":200,"data":{"playUrl":""}}`), nil
	})
	task := Task{
		DramaID: "sorani:101",
		Chapter: Chapter{ID: "sorani:101:9001", Source: sourceSorani,
			VideoURL: "sorani-play://9001"},
	}
	_, err := downloader.resolveSoraniMedia(context.Background(), task)
	if err == nil || !strings.Contains(err.Error(), "暂无可用播放地址") {
		t.Fatalf("空地址应给出明确提示，实际 %v", err)
	}
}

func TestCanonicalProviderSourceRecognizesSorani(t *testing.T) {
	for _, name := range []string{
		"sorani", "青空", "青空次元", "青空动漫", "www.sorani.net", "api.sorani.cc",
	} {
		if got := canonicalProviderSource(name); got != sourceSorani {
			t.Errorf("%q 应归一成 %s，实际 %s", name, sourceSorani, got)
		}
	}
	if !isHuangguoProviderSource(sourceSorani) {
		t.Fatal("青空次元应被认作有效站源")
	}
	if got := providerSourceForURL("https://www.sorani.net/video/101"); got != sourceSorani {
		t.Errorf("站点域名应识别成青空次元，实际 %q", got)
	}
}

func TestSoraniCategoriesAreFixed(t *testing.T) {
	downloader := soraniFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		t.Errorf("固定分类不应发起网络请求")
		return sourceFixtureResponse(request, http.StatusOK, "{}"), nil
	})
	categories := downloader.fetchSoraniCategories()
	if len(categories) != 3 {
		t.Fatalf("应有 3 个固定分类，实际 %d", len(categories))
	}
	if categories[0].ID != "1" || categories[2].ID != "5" {
		t.Errorf("分类 ID 错误: %+v", categories)
	}
}
