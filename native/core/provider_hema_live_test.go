package core

import (
	"context"
	"os"
	"testing"
	"time"
)

// 河马剧场（hema）真机实测。
//
// fixture 单测（`provider_hema_test.go`）保证"解析逻辑对"，这个用例回答另一个
// 问题：**上游还活着吗、字段还叫这些名字吗、付费集还换得出直链吗**。
// 默认跳过，`HEMA_LIVE_TEST=1` 时才跑。
//
// 它守住 0.8.17 接入时最容易悄悄退化的那条链路：
// 分类目录 → 详情（网页只给前 5 集）→ App 门户接口换直链 → 地址可拉流。
func TestHemaLiveCatalogDetailPlayback(t *testing.T) {
	if os.Getenv("HEMA_LIVE_TEST") != "1" {
		t.Skip("set HEMA_LIVE_TEST=1 to test the live 河马剧场 catalog and playback")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatalf("初始化原生引擎失败: %v", err)
	}
	d := engine.downloader

	// 1) 分类：上游 typeThreeList 有上百项，必须能解析出可用分类。
	categories, err := d.fetchHemaCategories(ctx)
	if err != nil {
		t.Fatalf("分类拉取失败: %v", err)
	}
	if len(categories) == 0 {
		t.Fatal("分类为空")
	}
	if len(categories) > hemaMaxCategories {
		t.Fatalf("分类应截断到 %d 个，实际 %d", hemaMaxCategories, len(categories))
	}
	t.Logf("分类 %d 个，前三个 = %s / %s / %s", len(categories), categories[0].Name, categories[1].Name, categories[2].Name)

	// 2) 目录：「全部」第一页应有 10 条。
	items, more, err := d.fetchHemaCatalogPage(ctx, 1, "", "")
	if err != nil {
		t.Fatalf("目录拉取失败: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("目录返回 0 部剧")
	}
	if !more {
		t.Errorf("「全部」第一页应判定还有更多")
	}
	for _, item := range items {
		if item.ID == "" || item.Title == "" || item.Source != sourceHema {
			t.Fatalf("目录记录缺字段: %+v", item)
		}
	}
	t.Logf("目录 %d 部，首部 = %s（%s）", len(items), items[0].Title, items[0].ID)

	// 3) 搜索。
	found, _, err := d.fetchHemaCatalogPage(ctx, 1, "", "总裁")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("搜索「总裁」返回 0 部剧")
	}
	t.Logf("搜索「总裁」命中 %d 部，首部 = %s", len(found), found[0].Title)

	// 4) 详情：找一部集数够多的，付费集（第 6 集之后）也要在分集表里。
	var (
		drama    Drama
		chapters []Chapter
	)
	for _, item := range items {
		candidate, list, detailErr := d.fetchHemaDetail(ctx, item.SourceID)
		if detailErr == nil && len(list) >= 6 {
			drama, chapters = candidate, list
			break
		}
	}
	if len(chapters) < 6 {
		t.Fatal("没找到集数 ≥ 6 的剧，无法验证付费集取流")
	}
	t.Logf("详情 %s: %d 集，第 1 集 = %s，第 6 集 = %s", drama.Title, len(chapters), chapters[0].Title, chapters[5].Title)

	// 5) 播放：第 1 集（网页免费）与第 6 集（网页付费，只能走 App 门户接口）
	//    都必须换得出可拉流的直链。
	for _, index := range []int{0, 5} {
		chapter := chapters[index]
		media, resolveErr := d.resolveHemaMedia(ctx, Task{DramaID: drama.ID, Chapter: chapter})
		if resolveErr != nil {
			t.Fatalf("第 %d 集换取播放地址失败: %v", index+1, resolveErr)
		}
		if !isProviderHTTPMediaURL(media.URL) {
			t.Fatalf("第 %d 集播放地址不是 http(s): %q", index+1, media.URL)
		}
		resolved, resolveErr := d.resolveProviderMedia(ctx, Task{DramaID: drama.ID, Chapter: chapter})
		if resolveErr != nil {
			t.Fatalf("第 %d 集完整解析链路失败: %v", index+1, resolveErr)
		}
		if !isProviderHTTPMediaURL(resolved.URL) {
			t.Fatalf("第 %d 集解析后地址无效: %q", index+1, resolved.URL)
		}
		t.Logf("第 %d 集（%s）播放地址 = %s", index+1, chapter.Title, resolved.URL)
	}
}
