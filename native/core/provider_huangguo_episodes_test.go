package core

import (
	"fmt"
	"strings"
	"testing"
)

// 构造黄果详情页里"选集区"的 HTML。
//
// 真实页面是服务端渲染的一串链接：第 1 集是 `/video/<id>/`，
// 第 N 集是 `/video/<id>/ep-<n>/`。
func huangguoEpisodeListHTML(sourceID string, count int) string {
	var b strings.Builder
	b.WriteString(`<div class="vd-episode-list">`)
	for i := 1; i <= count; i++ {
		href := fmt.Sprintf("/video/%s/", sourceID)
		if i > 1 {
			href = fmt.Sprintf("/video/%s/ep-%d/", sourceID, i)
		}
		fmt.Fprintf(&b, `<a href="%s" title="第%d集">%02d</a>`, href, i, i)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// 0.8.11：用户反馈"外面显示 42 集，进内容页只有 2 集"。
//
// 根因是详情页优先读 videoInitialData.epPlaySrcs，而站点改版后这个字段
// 只下发部分集的直链（按需签发）。选集区链接才是全量来源。
func TestParseHuangguoAIEpisodesReadsWholeEpisodeList(t *testing.T) {
	raw := huangguoEpisodeListHTML("117", 42)
	episodes := parseHuangguoAIEpisodes(raw, "https://huangguoai.com/video/117/", "117")
	if len(episodes) != 42 {
		t.Fatalf("分集数量 = %d，期望 42", len(episodes))
	}
	for i, ep := range episodes {
		if ep.Index != i+1 {
			t.Fatalf("第 %d 条集号 = %d，期望 %d", i, ep.Index, i+1)
		}
	}
	if !strings.HasSuffix(episodes[1].URL, "/video/117/ep-2/") {
		t.Fatalf("第 2 集链接 = %q", episodes[1].URL)
	}
}

// epPlaySrcs 只给 2 条时，不能把分集截断成 2 集；
// 直链要按集号合并进对应的分集。
func TestMergeHuangguoAIEpisodesKeepsFullListWhenInitialDataPartial(t *testing.T) {
	paged := parseHuangguoAIEpisodes(
		huangguoEpisodeListHTML("117", 42),
		"https://huangguoai.com/video/117/",
		"117",
	)
	initial := []providerEpisode{
		{Key: "1", Title: "第1集", Index: 1, HLS: "https://cdn.example/1.m3u8"},
		{Key: "2", Title: "第2集", Index: 2, HLS: "https://cdn.example/2.m3u8"},
	}
	merged := mergeHuangguoAIEpisodes(paged, initial)
	if len(merged) != 42 {
		t.Fatalf("合并后分集数量 = %d，期望 42", len(merged))
	}
	if merged[0].HLS != "https://cdn.example/1.m3u8" {
		t.Fatalf("第 1 集直链 = %q", merged[0].HLS)
	}
	if merged[1].HLS != "https://cdn.example/2.m3u8" {
		t.Fatalf("第 2 集直链 = %q", merged[1].HLS)
	}
	// 选集区已经覆盖到的集不应重复追加。
	seen := map[int]bool{}
	for _, ep := range merged {
		if seen[ep.Index] {
			t.Fatalf("集号 %d 重复", ep.Index)
		}
		seen[ep.Index] = true
	}
}

// 选集区抓不到时才整份退回 epPlaySrcs，避免完全没分集。
func TestMergeHuangguoAIEpisodesFallsBackToInitialData(t *testing.T) {
	initial := []providerEpisode{
		{Key: "1", Title: "第1集", Index: 1, HLS: "https://cdn.example/1.m3u8"},
		{Key: "2", Title: "第2集", Index: 2, HLS: "https://cdn.example/2.m3u8"},
	}
	merged := mergeHuangguoAIEpisodes(nil, initial)
	if len(merged) != 2 {
		t.Fatalf("退路分集数量 = %d，期望 2", len(merged))
	}
}

// 选集区抓漏时，按详情页总集数补齐 —— 保证内页集数和列表卡片一致。
func TestPadHuangguoAIEpisodesFillsToTotal(t *testing.T) {
	paged := parseHuangguoAIEpisodes(
		huangguoEpisodeListHTML("117", 2),
		"https://huangguoai.com/detail/117/",
		"117",
	)
	padded := padHuangguoAIEpisodes(paged, "https://huangguoai.com/detail/117/", "117", 42)
	if len(padded) != 42 {
		t.Fatalf("补齐后分集数量 = %d，期望 42", len(padded))
	}
	for i, ep := range padded {
		if ep.Index != i+1 {
			t.Fatalf("第 %d 条集号 = %d，期望 %d", i, ep.Index, i+1)
		}
	}
	// 补齐出来的分集只带页面地址，播放地址留空（由 resolve 现场取流）。
	if padded[41].HLS != "" {
		t.Fatalf("补齐的第 42 集不应带直链，实际 %q", padded[41].HLS)
	}
	if !strings.HasSuffix(padded[41].URL, "/video/117/ep-42/") {
		t.Fatalf("第 42 集页面地址 = %q", padded[41].URL)
	}
	// 第 1 集在站点上就是 /video/<id>/，不带 ep-1。
	if !strings.HasSuffix(padded[0].URL, "/video/117/") {
		t.Fatalf("第 1 集页面地址 = %q", padded[0].URL)
	}
}

// 总集数未知（或已经够数）时不能凭空造分集。
func TestPadHuangguoAIEpisodesLeavesCompleteListAlone(t *testing.T) {
	paged := parseHuangguoAIEpisodes(
		huangguoEpisodeListHTML("117", 42),
		"https://huangguoai.com/detail/117/",
		"117",
	)
	for _, total := range []int{0, 42, 30} {
		if got := len(padHuangguoAIEpisodes(paged, "https://huangguoai.com/detail/117/", "117", total)); got != 42 {
			t.Fatalf("total=%d 时补成了 %d 集，期望保持 42", total, got)
		}
	}
}
