package core

// 0.8.25 第三批榜单解析的离线单测：fixture 取自真实站点页面的最小片段。

import (
	"strings"
	"testing"
)

const ezdmwRankingFixture = `<html><body>
<a href="/Index/fan_ranking.html?name=2026%E5%B9%B47%E6%9C%88" style="color:blue;">2026年7月</a>
<a href="/Index/bangumi/81018.html" target="_blank" style="clear:both;float:left;"><p><span>1</span><strong style="font-weight:bold;">转生贵族靠着鉴定技能一飞冲天</strong></em></p>
    <img src="https://www.ezdmw.org/Public/atlas_oss/Public/images/some_img/2154.jpg" />
    <strong style="position:relative;">&nbsp;&nbsp;轻改、奇幻、战斗</strong>
    <strong>&nbsp;&nbsp;2024年4月/2026年10月【连载中】</strong>
</a>
<a href="/Index/bangumi/95049.html" target="_blank"><p><span>2</span><strong style="font-weight:bold;">野生的最终BOSS出现了</strong></em></p>
    <img src="https://www.ezdmw.org/Public/atlas_oss/Public/images/some_img/2155.jpg" />
    <strong>&nbsp;&nbsp;2026年10月【连载中】</strong>
</a>
<a href="/Index/bangumi/98494.html" target="_blank"><p><span>3</span><strong style="font-weight:bold;">JOJO的奇妙冒险 飙马野郎</strong></em></p>
</a>
</body></html>`

func TestParseEZDMWRankingExtractsOrderedEntries(t *testing.T) {
	page, err := parseEZDMWRanking(ezdmwRankingFixture, "https://www.ezdmw.org")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("want 3 items, got %d", len(page.Items))
	}
	first := page.Items[0]
	if first.Rank != 1 || first.Drama.SourceID != "81018" || first.Drama.Title != "转生贵族靠着鉴定技能一飞冲天" {
		t.Fatalf("first entry wrong: %+v", first)
	}
	if first.Drama.ID != providerDramaID(sourceEZDMW, "81018") || first.Drama.Source != sourceEZDMW {
		t.Fatalf("drama identity wrong: %+v", first.Drama)
	}
	if page.Items[2].Rank != 3 || page.Items[2].Drama.Title != "JOJO的奇妙冒险 飙马野郎" {
		t.Fatalf("third entry wrong: %+v", page.Items[2])
	}
	// 封面在同一条目的 <img> 上，必须一起带出来（没有封面的榜单页很难看）。
	if nativeText(first.Drama.Cover) == "" {
		t.Fatalf("first entry lost cover: %+v", first.Drama)
	}
	if page.HasMore || page.TotalPages != 1 {
		t.Fatalf("single page board should not paginate: %+v", page)
	}
}

func TestParseEZDMWRankingRejectsBrokenMarkup(t *testing.T) {
	for name, body := range map[string]string{
		"empty":    "",
		"no-block": "<html><body>没有条目</body></html>",
		// 名次倒序（2 在 1 前）必须整份拒绝。
		"reversed": strings.Replace(ezdmwRankingFixture, "<span>1</span>", "<span>2</span>", 1),
	} {
		if _, err := parseEZDMWRanking(body, "https://www.ezdmw.org"); err == nil {
			t.Fatalf("%s markup was accepted", name)
		}
	}
}

func TestParseEZDMWRankingDeduplicatesRepeatedEntry(t *testing.T) {
	// 真实页面侧栏会重复出现同一个条目，重复只跳过，不整榜作废。
	body := strings.Replace(ezdmwRankingFixture, "/Index/bangumi/95049.html", "/Index/bangumi/81018.html", 1)
	page, err := parseEZDMWRanking(body, "https://www.ezdmw.org")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("want 2 items after dedupe, got %d", len(page.Items))
	}
	if page.Items[1].Rank != 3 || page.Items[1].Drama.SourceID != "98494" {
		t.Fatalf("second kept entry wrong: %+v", page.Items[1])
	}
}

const jcydmHotFixture = `<html><body>
<div class="module-paper-item module-item">
	<div class="module-paper-item-header"><i class="icon64 icon64 iicon-jl"></i>
		<h3 class="module-paper-item-title">日漫榜</h3></div>
	<div class="module-paper-item-main">
		<a href="/index.php/vod/detail/id/13686.html">
			<div class="module-paper-item-num module-paper-item-num-first">1</div>
			<div class="module-paper-item-info"><span class="module-paper-item-infotitle">拔作岛THE ANIMATION</span>
				<p>更新至第11集</p>
			</div>
			<div class="icon-arrow-right"></div>
		</a>
		<a href="/index.php/vod/detail/id/7040.html">
			<div class="module-paper-item-num module-paper-item-num-second">2</div>
			<div class="module-paper-item-info"><span class="module-paper-item-infotitle">我独自升级第二季-起于暗影-</span>
				<p>已完结</p>
			</div>
			<div class="icon-arrow-right"></div>
		</a>
	</div>
</div>
<div class="module-paper-item module-item">
	<div class="module-paper-item-header"><i class="icon64 icon64 iicon-jl"></i>
		<h3 class="module-paper-item-title">动漫剧场榜</h3></div>
	<div class="module-paper-item-main">
		<a href="/index.php/vod/detail/id/13891.html">
			<div class="module-paper-item-num module-paper-item-num-first">1</div>
			<div class="module-paper-item-info"><span class="module-paper-item-infotitle">罗小黑战记2</span>
				<p>HD国语</p>
			</div>
		</a>
	</div>
</div>
</body></html>`

func TestParseJcydmRankingPicksRequestedBoard(t *testing.T) {
	riman, ok := findRankingBoard("jcydm-riman")
	if !ok {
		t.Fatal("jcydm-riman board missing")
	}
	page, err := parseJcydmRanking(jcydmHotFixture, "https://www.jcydm1.com", riman)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("riman want 2 items, got %d", len(page.Items))
	}
	if page.Items[0].Rank != 1 || page.Items[0].Drama.Title != "拔作岛THE ANIMATION" {
		t.Fatalf("riman first entry wrong: %+v", page.Items[0])
	}
	if page.Items[0].Drama.Source != sourceJcydm || page.Items[0].Drama.SourceID != "13686" {
		t.Fatalf("riman identity wrong: %+v", page.Items[0].Drama)
	}
	if page.Items[0].Drama.Remark != "更新至第11集" || page.Items[0].Drama.ReleaseStatus != "ongoing" {
		t.Fatalf("riman remark/status wrong: %+v", page.Items[0].Drama)
	}

	juchang, _ := findRankingBoard("jcydm-juchang")
	page, err = parseJcydmRanking(jcydmHotFixture, "https://www.jcydm1.com", juchang)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("juchang want 1 item, got %d", len(page.Items))
	}
	if page.Items[0].Drama.Title != "罗小黑战记2" || page.Items[0].Drama.SourceID != "13891" {
		t.Fatalf("juchang entry wrong: %+v", page.Items[0])
	}
}

func TestParseJcydmRankingRejectsMissingBoard(t *testing.T) {
	board, _ := findRankingBoard("jcydm-riman")
	// 动漫剧场榜缺失（fixture 里删掉那一块）
	broken := strings.Replace(jcydmHotFixture, "动漫剧场榜", "别的榜", 1)
	if _, err := parseJcydmRanking(broken, "https://www.jcydm1.com", board); err != nil {
		t.Fatal("riman should still parse when other board missing")
	}
	juchang, _ := findRankingBoard("jcydm-juchang")
	if _, err := parseJcydmRanking(broken, "https://www.jcydm1.com", juchang); err == nil {
		t.Fatal("missing board was accepted")
	}
}

func TestBuildMaccmsRankingNumbersByOrder(t *testing.T) {
	board, ok := findRankingBoard("wangguo-top")
	if !ok {
		t.Fatal("wangguo-top board missing")
	}
	items := []Drama{
		{ID: "wangguo:1", Source: sourceWangguo, Title: "甲"},
		{ID: "wangguo:1", Source: sourceWangguo, Title: "甲重复"},
		{ID: "wangguo:2", Source: sourceWangguo, Title: "乙"},
		{ID: "", Source: sourceWangguo, Title: "无ID"},
	}
	page, err := buildMaccmsRanking(items, board)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("want 2 unique items, got %d", len(page.Items))
	}
	if page.Items[0].Rank != 1 || page.Items[0].Drama.Title != "甲" || page.Items[1].Rank != 2 {
		t.Fatalf("order wrong: %+v", page.Items)
	}
	if page.HasMore {
		t.Fatal("single page should not have more")
	}
	if _, err := buildMaccmsRanking(nil, board); err == nil {
		t.Fatal("empty list accepted")
	}
}

func TestRankingBoardsSourcesAreKnown(t *testing.T) {
	// 每个榜单的站源都必须是已注册的 provider 源，防止 ID 手滑。
	for _, board := range rankingBoards {
		if !isHuangguoProviderSource(board.Source) {
			t.Fatalf("board %s has unknown source %s", board.ID, board.Source)
		}
		if strings.TrimSpace(board.Name) == "" || strings.TrimSpace(board.Description) == "" {
			t.Fatalf("board %s missing name or description", board.ID)
		}
	}
}
