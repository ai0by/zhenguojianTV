package core

// 0.8.25 第三批：新接入站源榜单的联网冒烟测试。
//
// 设 RANK_LIVE_TEST=1 才会跑（与 MACCMS_LIVE_TEST 同一套思路：常规单测
// 保持确定性）。黄剧 / 51 需要访客会话或 Nuxt payload，境外 runner 大概率
// 不通，所以只在本地手动触发。

import (
	"context"
	"os"
	"testing"
	"time"
)

var rankLiveBoards = []string{
	"huangju-hot",
	"huangju-new",
	"51duanju-total",
	"51duanju-week",
	"51duanju-month",
	"ezdmw-season",
	"wangguo-top",
	"faguo-duanju",
	"faguo-anime",
	"jcydm-riman",
	"jcydm-juchang",
}

func TestRankingLiveFetch(t *testing.T) {
	if os.Getenv("RANK_LIVE_TEST") != "1" {
		t.Skip("set RANK_LIVE_TEST=1 to test the live ranking boards")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatalf("initialize native engine: %v", err)
	}
	d := engine.downloader

	for _, id := range rankLiveBoards {
		board, ok := findRankingBoard(id)
		if !ok {
			t.Fatalf("board %s missing", id)
		}
		page, err := d.loadRankingPage(ctx, board, 1, true)
		if err != nil {
			t.Errorf("%s (%s) 拉取失败：%v", board.ID, board.Name, err)
			continue
		}
		if len(page.Items) == 0 {
			t.Errorf("%s 榜单为空", board.ID)
			continue
		}
		first := page.Items[0]
		if first.Rank < 1 || first.Drama.ID == "" || first.Drama.Title == "" {
			t.Errorf("%s 首条字段不全：%+v", board.ID, first)
			continue
		}
		// 名次应递增。
		for i := 1; i < len(page.Items); i++ {
			if page.Items[i].Rank <= page.Items[i-1].Rank {
				t.Errorf("%s 名次非递增：%d -> %d", board.ID, page.Items[i-1].Rank, page.Items[i].Rank)
				break
			}
		}
		t.Logf("%s ✓ %d 条，首条 #%d %s", board.ID, len(page.Items), first.Rank, first.Drama.Title)
	}
}
