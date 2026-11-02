package core

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// 黄剧目录的分页边界（0.8.20）。
//
// 站点只服务到第 100 页（20 条/页 = 2000 部），但 `total` 报得更大。
// 请求第 101 页时它不报错，而是把页码夹回 100 再返回一遍 —— 以前这里
// 一律当「页码与请求不符」的错误，于是 catalogdump 判定整站不完整、
// 丢弃 2000 部退回旧的 1200 部。夹页其实是"没有下一页了"。
func huangjuCatalogFixture(page, rows int) string {
	items := make([]string, 0, rows)
	for index := 0; index < rows; index++ {
		items = append(items, fmt.Sprintf(`{"id":"%d","slug":"demo%d","title":"第%d部"}`, index+1, index+1, index+1))
	}
	return fmt.Sprintf(
		`{"data":{"items":[%s],"page":%d,"pageSize":20,"total":2400}}`,
		strings.Join(items, ","), page,
	)
}

func huangjuTestDownloader(t *testing.T, body string) *Downloader {
	t.Helper()
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		// 目录接口只走一个 GET，直接把 fixture 回给所有请求 —— 站源基址会经过
		// 镜像候选表，测试里不必也不该去关心它最终落在哪个域名。
		return sourceFixtureResponse(request, http.StatusOK, body), nil
	})
	// 客户端在第一次取实例时读 cfg，这里提前改指到假域名。
	downloader.cfg.HuangjuAPIURL = "https://huangju-api.test"
	// 访客授权换 token 的握手由 provider_huangju_client 自己负责，这里不测它：
	// 直接预置 token，让目录请求一步到位。
	client := downloader.huangjuClient()
	client.mu.Lock()
	client.token = "test-token"
	client.mu.Unlock()
	return downloader
}

func TestHuangjuCatalogClampedPageEndsTheListing(t *testing.T) {
	// 要第 101 页，站点回第 100 页 → 正常收尾，不是错误。
	downloader := huangjuTestDownloader(t, huangjuCatalogFixture(100, 0))
	items, more, err := downloader.fetchHuangjuCatalogPage(context.Background(), 101, "", "")
	if err != nil {
		t.Fatalf("夹页不该报错，得到 %v", err)
	}
	if more {
		t.Fatal("夹页之后不该还有下一页")
	}
	if len(items) != 0 {
		t.Fatalf("夹页应该返回空列表，得到 %d 条", len(items))
	}
}

func TestHuangjuCatalogRejectsPageJumpingForward(t *testing.T) {
	// 站点回了一个比请求更大的页码 → 这是异常，必须报错（不能当成有数据）。
	downloader := huangjuTestDownloader(t, huangjuCatalogFixture(102, 1))
	if _, _, err := downloader.fetchHuangjuCatalogPage(context.Background(), 101, "", ""); err == nil {
		t.Fatal("页码比请求更大时必须报错")
	}
}

func TestHuangjuCatalogAcceptsMatchingPage(t *testing.T) {
	downloader := huangjuTestDownloader(t, huangjuCatalogFixture(3, 1))
	items, more, err := downloader.fetchHuangjuCatalogPage(context.Background(), 3, "", "")
	if err != nil {
		t.Fatalf("正常页不该报错，得到 %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("应解析出 1 条，得到 %d 条", len(items))
	}
	// total 2400 / pageSize 20 = 120 页，第 3 页还有下一页。
	if !more {
		t.Fatal("第 3 页之后应还有下一页")
	}
}
