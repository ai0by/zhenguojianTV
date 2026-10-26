package core

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// 苹果CMS 采集站的联网冒烟测试。
//
// 设 MACCMS_LIVE_TEST=1 才会跑，常规单测保持确定性（离线 fixture）。
// 四个站（花果 / 网果 / 发果 / 伍果）的目录 → 详情 → 播放地址整条链路都验，
// 加新站时把新站加进 maccmsLiveSources 就能一起验。
var maccmsLiveSources = []string{sourceHuaguo, sourceWangguo, sourceFaguo, sourceWuguo}

func TestMaccmsLiveCatalogDetailPlayback(t *testing.T) {
	if os.Getenv("MACCMS_LIVE_TEST") != "1" {
		t.Skip("set MACCMS_LIVE_TEST=1 to test the live 苹果CMS catalog and playback")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatalf("initialize native engine: %v", err)
	}
	d := engine.downloader

	for _, source := range maccmsLiveSources {
		profile, found := maccmsProfileFor(source)
		if !found {
			t.Fatalf("%s 没有 profile", source)
		}
		items, hasMore, err := d.fetchMaccmsCatalogPage(ctx, source, 1, "")
		if err != nil {
			t.Fatalf("%s 目录抓取失败：%v", profile.Name, err)
		}
		if len(items) < 10 {
			t.Fatalf("%s 目录只解析出 %d 部剧", profile.Name, len(items))
		}
		if !hasMore {
			t.Fatalf("%s 第 1 页 hasMore 应为 true", profile.Name)
		}
		first := items[0]
		if first.Title == "" || nativeText(first.Cover) == "" || first.SourceID == "" {
			t.Fatalf("%s 首条卡片字段不全：%+v", profile.Name, first)
		}

		drama, chapters, err := d.fetchMaccmsDetail(ctx, source, first.SourceID)
		if err != nil {
			t.Fatalf("%s 详情抓取失败（%s）：%v", profile.Name, first.Title, err)
		}
		if len(chapters) == 0 {
			t.Fatalf("%s 详情 %q 没有分集", profile.Name, drama.DisplayTitle())
		}
		if drama.DisplayTitle() == "" || nativeText(drama.Cover) == "" {
			t.Fatalf("%s 详情字段不全：%+v", profile.Name, drama)
		}

		task := Task{DramaID: drama.ID, DramaTitle: drama.DisplayTitle(), Chapter: chapters[0]}
		media, err := d.resolveMaccmsPlayback(ctx, task)
		if err != nil {
			t.Fatalf("%s 播放地址解析失败（%q）：%v", profile.Name, drama.DisplayTitle(), err)
		}
		if !isProviderHTTPMediaURL(media.URL) || !strings.Contains(strings.ToLower(media.URL), ".m3u8") {
			t.Fatalf("%s 播放地址异常：%q", profile.Name, media.URL)
		}
		t.Logf("%s ✓ %s / %s / %d 集 / %s", profile.Name, first.Title, drama.DisplayTitle(), len(chapters), media.URL)
	}
}
