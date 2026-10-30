package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// 河马剧场（hema）站源单测。
//
// 全部走 fixture：请求形状、页面数据解析、分类排序、以及最关键的
// 「付费集按 chapterId 走 App 门户接口换直链」这条链路。
//
// 两个基址是变量（hemaSiteBaseURL / hemaAppAPIBaseURL），测试里改指到
// `https://hema.test`，这样 providerSourceForURL 认不出域名，候选域名只有
// 一个，fixture 断言不会被远程镜像配置干扰。

const hemaCatalogFixture = `{"props":{"pageProps":{"typeThreeId":"1170","typeName":"青春",
 "page":1,"pages":27,"typeThreeList":[],
 "bookList":[
  {"bookId":"41000454424","bookName":"邻里的边界",
   "coverWap":"https://seoali.example.test/cppartner/41000454424.jpg",
   "introduction":"电梯加装引发的邻里反击。","totalChapterNum":"63","followCount":"4.47万",
   "bookTypeThree":[{"id":1648,"name":"3D漫"},{"id":1170,"name":"青春"}],
   "actor":"陈默","actress":"王翠花","status":1,"statusDesc":"完本"},
  {"bookId":"41000454425","bookName":"第二部","coverWap":"https://seoali.example.test/2.jpg",
   "introduction":"","totalChapterNum":"12","bookTypeThree":[]}
 ]}}}`

const hemaDetailFixture = `{"props":{"pageProps":{"typeThreeList":[],
 "bookInfoVo":{"bookId":"41000115794","bookName":"绝命老父",
  "coverWap":"https://seoali.example.test/41000115794.jpg",
  "introduction":"肺癌晚期的王忠被金姐收为黑手套。","totalChapterNum":"3",
  "bookTypeThree":[{"id":416,"name":"复仇"},{"id":417,"name":"逆袭"}],
  "actor":"朱新凡","actress":"顾沅真","status":1,"statusDesc":"完本"},
 "chapterList":[
  {"chapterId":"592904338","chapterName":"第一集","isCharge":"0","chapterIndex":1,
   "chapterVideoVo":{"mp4":"https://dzzt-video.example.test/1.mp4"}},
  {"chapterId":"592904343","chapterName":"第二集","isCharge":"1","chapterIndex":2},
  {"chapterId":"592904388","chapterName":"第三集","isCharge":"1","chapterIndex":3}
 ]}}}`

const hemaSearchFixture = `{"retCode":0,"data":{"isMore":1,"totalSize":304,"bookList":[
 {"bookId":"41000358232","bookName":"真假总裁","coverWap":"https://seoali.example.test/3.jpg",
  "introduction":"保姆儿子冒充总裁。","totalChapterNum":"96","bookTypeThree":[{"id":416,"name":"复仇"}]}
]}}`

// hemaTestDownloader 把两个基址指到假域名，并返回一个 fixture Downloader。
func hemaTestDownloader(t *testing.T, handler sourceFixtureTransport) *Downloader {
	t.Helper()
	previousSite, previousAPI := hemaSiteBaseURL, hemaAppAPIBaseURL
	hemaSiteBaseURL, hemaAppAPIBaseURL = "https://hema.test", "https://hema-api.test"
	t.Cleanup(func() { hemaSiteBaseURL, hemaAppAPIBaseURL = previousSite, previousAPI })
	return sourceFixtureDownloader(t, handler)
}

// hemaPage 把整份 `__NEXT_DATA__` 根对象（含 props.pageProps）包成一份 HTML。
func hemaPage(t *testing.T, raw string) string {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		t.Fatalf("fixture 不是合法 JSON: %v", err)
	}
	if _, ok := root["props"].(map[string]any); !ok {
		t.Fatalf("fixture 必须是 {\"props\":{\"pageProps\":…}} 结构")
	}
	return `<!DOCTYPE html><html><head></head><body><div id="__next"></div>` +
		`<script id="__NEXT_DATA__" type="application/json">` + raw + `</script></body></html>`
}

// hemaEncrypted 把明文按门户接口的约定加密成 `data` 字段。
func hemaEncrypted(t *testing.T, plain string) string {
	t.Helper()
	encrypted, err := aesCBCEncrypt([]byte(plain), hemaAppKey, hemaAppIV)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	return base64.StdEncoding.EncodeToString(encrypted)
}

func TestHemaCatalogParsesBookList(t *testing.T) {
	var paths []string
	downloader := hemaTestDownloader(t, func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		return sourceFixtureResponse(request, http.StatusOK, hemaPage(t, hemaCatalogFixture)), nil
	})

	items, more, err := downloader.fetchHemaCatalogPage(context.Background(), 1, "1170", "")
	if err != nil {
		t.Fatalf("目录解析失败: %v", err)
	}
	if len(paths) != 1 || paths[0] != "/browse/1170/1" {
		t.Fatalf("应只请求 /browse/1170/1，实际 %v", paths)
	}
	if len(items) != 2 {
		t.Fatalf("应解析出 2 部剧，实际 %d", len(items))
	}
	first := items[0]
	if first.ID != "hema:41000454424" || first.Source != sourceHema || first.SourceID != "41000454424" {
		t.Errorf("ID 映射错误: %+v", first)
	}
	if first.Title != "邻里的边界" || first.Desc != "电梯加装引发的邻里反击。" {
		t.Errorf("标题 / 简介映射错误: %+v", first)
	}
	if !strings.HasPrefix(nativeText(first.Cover), "https://") {
		t.Errorf("封面应是绝对地址，实际 %v", first.Cover)
	}
	if nativeText(first.EpisodeCount) != "63" {
		t.Errorf("集数应为 63，实际 %v", first.EpisodeCount)
	}
	if first.ChannelName != "河马剧场" {
		t.Errorf("频道名错误: %q", first.ChannelName)
	}
	if !containsTag(first.Tags, "3D漫") || !containsTag(first.Tags, "主演 陈默") {
		t.Errorf("标签映射错误: %v", first.Tags)
	}
	if !more {
		t.Errorf("page=1 / pages=27 应判定还有更多")
	}
}

func TestHemaCatalogStopsAtLastPage(t *testing.T) {
	fixture := `{"props":{"pageProps":{"page":27,"pages":27,"bookList":[
	 {"bookId":"41000454424","bookName":"末页剧","coverWap":"https://seoali.example.test/1.jpg"}]}}}`
	downloader := hemaTestDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusOK, hemaPage(t, fixture)), nil
	})
	items, more, err := downloader.fetchHemaCatalogPage(context.Background(), 27, "", "")
	if err != nil {
		t.Fatalf("目录解析失败: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("应解析出 1 部剧，实际 %d", len(items))
	}
	if more {
		t.Errorf("page=pages 时不应再判定有更多")
	}
}

func TestHemaCatalogUsesZeroForAllCategory(t *testing.T) {
	var paths []string
	downloader := hemaTestDownloader(t, func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		return sourceFixtureResponse(request, http.StatusOK, hemaPage(t, hemaCatalogFixture)), nil
	})
	if _, _, err := downloader.fetchHemaCatalogPage(context.Background(), 2, "", ""); err != nil {
		t.Fatalf("目录解析失败: %v", err)
	}
	if len(paths) != 1 || paths[0] != "/browse/0/2" {
		t.Fatalf("「全部」应走 /browse/0/<page>，实际 %v", paths)
	}
}

func TestHemaCatalogRejectsInvalidCategory(t *testing.T) {
	downloader := hemaTestDownloader(t, func(request *http.Request) (*http.Response, error) {
		t.Fatalf("非法分类不应发起请求，实际请求 %s", request.URL)
		return nil, nil
	})
	if _, _, err := downloader.fetchHemaCatalogPage(context.Background(), 1, "../etc", ""); err == nil {
		t.Fatal("非法分类应报错")
	}
}

func TestHemaDetailBuildsChapterPlaceholders(t *testing.T) {
	var paths []string
	downloader := hemaTestDownloader(t, func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		return sourceFixtureResponse(request, http.StatusOK, hemaPage(t, hemaDetailFixture)), nil
	})

	drama, chapters, err := downloader.fetchHemaDetail(context.Background(), "41000115794")
	if err != nil {
		t.Fatalf("详情解析失败: %v", err)
	}
	if len(paths) != 1 || paths[0] != "/drama/41000115794" {
		t.Fatalf("应只请求 /drama/41000115794，实际 %v", paths)
	}
	if drama.Title != "绝命老父" || drama.SourceID != "41000115794" {
		t.Errorf("详情映射错误: %+v", drama)
	}
	if len(chapters) != 3 {
		t.Fatalf("应有 3 集，实际 %d", len(chapters))
	}
	// 付费集（isCharge=1，网页没有 chapterVideoVo）也必须进分集表 ——
	// 地址在播放阶段由 App 门户接口现取。
	for index, chapter := range chapters {
		if chapter.Source != sourceHema {
			t.Errorf("第 %d 集站源错误: %q", index+1, chapter.Source)
		}
		if !strings.HasPrefix(chapter.VideoURL, "hema-play://") {
			t.Errorf("第 %d 集应存 hema-play:// 占位，实际 %q", index+1, chapter.VideoURL)
		}
		if chapter.EpisodeString(index+1) != fmt.Sprint(index+1) {
			t.Errorf("第 %d 集集号错误: %q", index+1, chapter.EpisodeString(index+1))
		}
	}
	if chapters[0].ID != "hema:41000115794:592904338" {
		t.Errorf("分集 ID 拼接错误: %q", chapters[0].ID)
	}
	if chapters[1].VideoURL != "hema-play://592904343" {
		t.Errorf("第二集占位地址错误: %q", chapters[1].VideoURL)
	}
}

func TestHemaDetailRejectsMismatchedDrama(t *testing.T) {
	downloader := hemaTestDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusOK, hemaPage(t, hemaDetailFixture)), nil
	})
	if _, _, err := downloader.fetchHemaDetail(context.Background(), "99999999999"); err == nil {
		t.Fatal("详情与请求 ID 不符时应报错")
	}
}

func TestHemaSearchPostsKeyword(t *testing.T) {
	var (
		method  string
		path    string
		body    map[string]any
		pname   string
		referer string
	)
	downloader := hemaTestDownloader(t, func(request *http.Request) (*http.Response, error) {
		method, path = request.Method, request.URL.Path
		pname = request.Header.Get("pname")
		referer = request.Header.Get("Referer")
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("搜索请求体不是 JSON: %v", err)
		}
		return sourceFixtureResponse(request, http.StatusOK, hemaSearchFixture), nil
	})

	items, more, err := downloader.fetchHemaCatalogPage(context.Background(), 2, "", "总裁")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if method != http.MethodPost || path != "/seo/video/6007" {
		t.Fatalf("搜索应 POST /seo/video/6007，实际 %s %s", method, path)
	}
	if nativeText(body["keyword"]) != "总裁" || nativeText(body["index"]) != "2" {
		t.Errorf("搜索参数错误: %v", body)
	}
	if nativeText(body["sourceType"]) != "1" {
		t.Errorf("sourceType 应为 1，实际 %v", body["sourceType"])
	}
	if pname != "www.kuaikaw.cn" {
		t.Errorf("必须带 pname 头（上游按它识别站点），实际 %q", pname)
	}
	if !strings.HasSuffix(referer, "/search") {
		t.Errorf("搜索的 Referer 应指向 /search，实际 %q", referer)
	}
	if len(items) != 1 || items[0].Title != "真假总裁" {
		t.Fatalf("搜索结果映射错误: %+v", items)
	}
	if !more {
		t.Errorf("isMore=1 时应判定还有更多")
	}
}

func TestHemaSearchRejectsErrorCode(t *testing.T) {
	downloader := hemaTestDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusOK, `{"retCode":1001,"data":null}`), nil
	})
	if _, _, err := downloader.fetchHemaCatalogPage(context.Background(), 1, "", "总裁"); err == nil {
		t.Fatal("retCode 非 0 时应报错")
	}
}

func TestHemaResolveUsesAppPortalForPaidChapter(t *testing.T) {
	// 付费集：网页上根本没有 chapterVideoVo，地址只能由门户接口给。
	plain := `{"bookId":"41000115794","maxChapter":91,"chapterInfo":[
	 {"chapterId":"592904343","chapterName":"第六集","chapterIndex":6,"content":{
	   "mp4SwitchUrl":["https://txonlmfvideo.example.test/592904343.720p.mp4?sign=abc"],
	   "mp4720p":"https://mfvideo.example.test/592904343.720p.mp4",
	   "mp4Url":"https://mfvideo.example.test/592904343.720p.mp4"}}]}`
	var (
		path     string
		alg      string
		datas    string
		bodyText string
	)
	downloader := hemaTestDownloader(t, func(request *http.Request) (*http.Response, error) {
		path = request.URL.Path
		alg = request.Header.Get("alg")
		datas = request.Header.Get("datas")
		raw, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Fatalf("读取请求体失败: %v", readErr)
		}
		bodyText = string(raw)
		response := fmt.Sprintf(`{"code":0,"data":%q}`, hemaEncrypted(t, plain))
		return sourceFixtureResponse(request, http.StatusOK, response), nil
	})

	task := Task{
		DramaID: "hema:41000115794",
		Chapter: Chapter{
			ID: "hema:41000115794:592904343", Source: sourceHema,
			VideoURL: "hema-play://592904343",
		},
	}
	media, err := downloader.resolveHemaMedia(context.Background(), task)
	if err != nil {
		t.Fatalf("换取播放地址失败: %v", err)
	}
	if path != "/free-video-portal/portal/1139" {
		t.Errorf("应请求门户接口 1139，实际 %q", path)
	}
	if alg != "HG45LKBS" {
		t.Errorf("必须带 alg 头，实际 %q", alg)
	}
	if datas == "" {
		t.Error("必须带加密后的 datas 头")
	}
	if !isProviderHTTPMediaURL(media.URL) || !strings.Contains(media.URL, "txonlmfvideo.example.test") {
		t.Errorf("应优先使用 mp4SwitchUrl，实际 %q", media.URL)
	}

	// 请求体也是加密的：解出来必须是 {bookId, chapterIds, chapterId, ...}。
	decoded, err := hemaAESDecryptString(bodyText)
	if err != nil {
		t.Fatalf("请求体无法解密: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(decoded), &payload); err != nil {
		t.Fatalf("请求体不是 JSON: %v", err)
	}
	if nativeText(payload["bookId"]) != "41000115794" {
		t.Errorf("bookId 错误: %v", payload["bookId"])
	}
	if ids, ok := payload["chapterIds"].([]any); !ok || len(ids) != 1 || nativeText(ids[0]) != "592904343" {
		t.Errorf("chapterIds 错误: %v", payload["chapterIds"])
	}
	if nativeText(payload["resolutionRate"]) != "720P" {
		t.Errorf("清晰度应为 720P，实际 %v", payload["resolutionRate"])
	}
}

func TestHemaResolveFallsBackToMp4Fields(t *testing.T) {
	plain := `{"chapterInfo":[{"chapterId":"592904343","content":{
	 "mp4SwitchUrl":["","not-a-url"],
	 "mp4720p":"https://mfvideo.example.test/592904343.720p.mp4"}}]}`
	downloader := hemaTestDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusOK,
			fmt.Sprintf(`{"code":0,"data":%q}`, hemaEncrypted(t, plain))), nil
	})
	task := Task{
		DramaID: "hema:41000115794",
		Chapter: Chapter{ID: "hema:41000115794:592904343", Source: sourceHema, VideoURL: "hema-play://592904343"},
	}
	media, err := downloader.resolveHemaMedia(context.Background(), task)
	if err != nil {
		t.Fatalf("换取播放地址失败: %v", err)
	}
	if media.URL != "https://mfvideo.example.test/592904343.720p.mp4" {
		t.Errorf("应回落到 mp4720p，实际 %q", media.URL)
	}
}

func TestHemaResolveRejectsEmptyContent(t *testing.T) {
	downloader := hemaTestDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusOK,
			fmt.Sprintf(`{"code":0,"data":%q}`, hemaEncrypted(t, `{"chapterInfo":[{"chapterId":"592904343"}]}`))), nil
	})
	task := Task{
		DramaID: "hema:41000115794",
		Chapter: Chapter{ID: "hema:41000115794:592904343", Source: sourceHema, VideoURL: "hema-play://592904343"},
	}
	if _, err := downloader.resolveHemaMedia(context.Background(), task); err == nil {
		t.Fatal("门户接口没有 content 时应报错")
	}
}

func TestHemaResolveRejectsForeignChapter(t *testing.T) {
	downloader := hemaTestDownloader(t, func(request *http.Request) (*http.Response, error) {
		t.Fatalf("分集 ID 与剧集不符时不应发起请求，实际请求 %s", request.URL)
		return nil, nil
	})
	task := Task{
		DramaID: "hema:41000115794",
		Chapter: Chapter{ID: "hema:41000115794", Source: sourceHema, VideoURL: "hema-play://"},
	}
	if _, err := downloader.resolveHemaMedia(context.Background(), task); err == nil {
		t.Fatal("分集 ID 缺 chapterId 时应报错")
	}
}

func TestHemaCategoriesPreferHotNames(t *testing.T) {
	rows := []any{
		map[string]any{"name": "全部", "typeThreeIds": []any{json.Number("0")}},
		map[string]any{"name": "青春", "typeThreeIds": []any{json.Number("1170")}},
		map[string]any{"name": "甜宠", "typeThreeIds": []any{json.Number("462")}},
		map[string]any{"name": "萌宝", "typeThreeIds": []any{json.Number("589"), json.Number("1175")}},
		map[string]any{"name": "逆袭", "typeThreeIds": []any{json.Number("417"), json.Number("464")}},
	}
	categories := hemaCategoriesFromList(rows)
	if len(categories) != 4 {
		t.Fatalf("应得到 4 个分类（「全部」由 nativeCategories 自带），实际 %d: %+v", len(categories), categories)
	}
	// 热门名提前（顺序跟 hemaPreferredCategoryNames 一致），其余保持站点原序。
	got := []string{categories[0].Name, categories[1].Name, categories[2].Name, categories[3].Name}
	want := []string{"甜宠", "青春", "逆袭", "萌宝"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("分类顺序应为 %v，实际 %v", want, got)
		}
	}
	if categories[0].ID != "462" || categories[2].ID != "417" {
		t.Errorf("多 ID 分类应取第一个非 0 ID: %+v", categories)
	}
}

func TestHemaCategoriesCapped(t *testing.T) {
	rows := make([]any, 0, 200)
	for index := 0; index < 200; index++ {
		rows = append(rows, map[string]any{
			"name":         fmt.Sprintf("分类%d", index),
			"typeThreeIds": []any{json.Number(fmt.Sprint(index + 1))},
		})
	}
	categories := hemaCategoriesFromList(rows)
	if len(categories) != hemaMaxCategories {
		t.Fatalf("分类应截断到 %d 个，实际 %d", hemaMaxCategories, len(categories))
	}
}

func TestHemaCategoriesFromLiveShape(t *testing.T) {
	// 真实站点的 typeThreeList 片段（2026-10-30 实测）。
	fixture := `{"props":{"pageProps":{"bookList":[],"typeThreeList":[
	 {"typeThreeIds":[0],"name":"全部"},
	 {"name":"青春","typeThreeIds":[1170]},
	 {"name":"萌宝","typeThreeIds":[589,1175]},
	 {"name":"甜宠","typeThreeIds":[462]}]}}}`
	downloader := hemaTestDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusOK, hemaPage(t, fixture)), nil
	})
	categories, err := downloader.fetchHemaCategories(context.Background())
	if err != nil {
		t.Fatalf("分类拉取失败: %v", err)
	}
	if len(categories) != 3 {
		t.Fatalf("应得到 3 个分类，实际 %d: %+v", len(categories), categories)
	}
	if categories[0].Name != "甜宠" || categories[0].ID != "462" {
		t.Errorf("热门分类应排第一: %+v", categories)
	}
}

func containsTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}
