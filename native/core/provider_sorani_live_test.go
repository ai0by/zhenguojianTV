package core

import (
	"context"
	"os"
	"testing"
	"time"
)

// 青空次元（sorani）真机实测。
//
// 本机沙箱连不上 `api.sorani.cc`，所以解析逻辑由 `provider_sorani_test.go`
// 的 fixture 单测覆盖；这个用例负责回答另一个问题：**上游还活着吗、
// 字段还叫这些名字吗**。默认跳过，`SORANI_LIVE_TEST=1` 时才跑，
// 由 `.github/workflows/sorani-live-smoke.yml` 在 GitHub runner 上触发。
//
// 它同时守住一条容易悄悄退化的链路：
// 目录 → 详情（episodeId）→ play 接口换出 m3u8 → 播放列表可解析。
func TestSoraniLiveCatalogDetailPlayback(t *testing.T) {
	if os.Getenv("SORANI_LIVE_TEST") != "1" {
		t.Skip("set SORANI_LIVE_TEST=1 to test the live 青空次元 catalog and playback")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatalf("初始化原生引擎失败: %v", err)
	}
	d := engine.downloader

	// 1) 目录：三个固定分类各自第一页都应有数据。
	for _, category := range soraniCategories {
		items, _, err := d.fetchSoraniCatalogPage(ctx, 1, category.ID, "")
		if err != nil {
			t.Fatalf("目录分类 %s(%s) 拉取失败: %v", category.Name, category.ID, err)
		}
		if len(items) == 0 {
			t.Fatalf("目录分类 %s(%s) 返回 0 部剧", category.Name, category.ID)
		}
		for _, item := range items {
			if item.ID == "" || item.Title == "" || item.Source != sourceSorani {
				t.Fatalf("目录记录缺字段: %+v", item)
			}
		}
		t.Logf("分类 %s: %d 部，首部 = %s（%s）", category.Name, len(items), items[0].Title, items[0].ID)
	}

	// 2) 详情：拿一部有分集的剧。
	items, _, err := d.fetchSoraniCatalogPage(ctx, 1, soraniCategories[0].ID, "")
	if err != nil {
		t.Fatalf("目录拉取失败: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("目录为空，无法继续测详情")
	}
	var (
		drama    Drama
		chapters []Chapter
	)
	for _, item := range items {
		drama, chapters, err = d.fetchSoraniDetail(ctx, item.SourceID)
		if err == nil && len(chapters) > 0 {
			break
		}
	}
	if err != nil {
		t.Fatalf("详情拉取失败: %v", err)
	}
	if len(chapters) == 0 {
		t.Fatal("详情没有任何可播放分集")
	}
	t.Logf("详情 %s: %d 集，第 1 集 = %s（%s）", drama.Title, len(chapters), chapters[0].Title, chapters[0].ID)

	// 3) 播放：分集里存的是 sorani-play:// 占位，必须能换成真实地址。
	media, err := d.resolveSoraniMedia(ctx, Task{DramaID: drama.ID, Chapter: chapters[0]})
	if err != nil {
		t.Fatalf("换取播放地址失败: %v", err)
	}
	if !isProviderHTTPMediaURL(media.URL) {
		t.Fatalf("播放地址不是 http(s): %q", media.URL)
	}
	t.Logf("播放地址 = %s", media.URL)

	// 4) 走一遍完整的解析链路（会拉 m3u8 并解析时长）。
	resolved, err := d.resolveProviderMedia(ctx, Task{DramaID: drama.ID, Chapter: chapters[0]})
	if err != nil {
		t.Fatalf("完整解析链路失败: %v", err)
	}
	if resolved.Playlist != "" {
		if resolved.Duration <= 0 {
			t.Fatalf("播放列表已解析但时长为 0，可能不是正片")
		}
		t.Logf("播放列表 %d 字节，时长 %s", len(resolved.Playlist), resolved.Duration)
	} else {
		t.Logf("直链播放（非 m3u8）")
	}
}
