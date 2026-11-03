package core

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// hongguoDanmakuBody 造一份评论接口的响应。
//
// rows 是 "评论 id,偏移毫秒,文本" 三元组，next 是 next_query_danmaku_list_time
// 的原始字符串（传空串表示服务端没下发这个字段）。
func hongguoTestDanmakuBody(videoID string, next string, rows ...string) string {
	items := make([]string, 0, len(rows))
	for _, row := range rows {
		parts := strings.SplitN(row, ",", 3)
		items = append(items, fmt.Sprintf(
			`{"comment":{"comment_id":%q,"common":{"group_id":%q,"status":"1","content":{"text":%q}},"expand":{"offset_time":%q}}}`,
			parts[0], videoID, parts[2], parts[1]))
	}
	extra := "{}"
	if next != "" {
		extra = fmt.Sprintf(`{"next_query_danmaku_list_time":%q}`, next)
	}
	return fmt.Sprintf(`{"code":0,"data":{"data_list":[%s],"extra":%s}}`, strings.Join(items, ","), extra)
}

// 空窗口不是错误：片头片尾没有任何弹幕时，服务端可能不下发 next 字段。
// 以前这里判"格式无效"，前端会把这一窗标成"弹幕暂不可用"并反复重试。
func TestParseHongguoDanmakuEmptyWindow(t *testing.T) {
	page, err := parseHongguoDanmaku(map[string]any{
		"data": map[string]any{"data_list": []any{}},
	}, "7001", 60_000, 120_000)
	if err != nil {
		t.Fatalf("空窗口应视为成功：%v", err)
	}
	if page.StartMS != 60_000 || page.NextMS != 90_000 {
		t.Fatalf("空窗口应按 30 秒推进，实际 start=%d next=%d", page.StartMS, page.NextMS)
	}
	if len(page.Items) != 0 || page.EpisodeID != "7001" {
		t.Fatalf("空窗口不该有弹幕：%+v", page)
	}
	// 片尾不足 30 秒时窗口要收在时长上。
	tail, err := parseHongguoDanmaku(map[string]any{
		"data": map[string]any{"data_list": []any{}},
	}, "7001", 110_000, 120_000)
	if err != nil {
		t.Fatalf("片尾空窗口应成功：%v", err)
	}
	if tail.NextMS != 120_000 {
		t.Fatalf("片尾窗口应收在时长上，实际 %d", tail.NextMS)
	}
}

// 有数据但没有 next 字段才是真的异常（无法确定窗口边界）。
func TestParseHongguoDanmakuMissingNextWithRows(t *testing.T) {
	_, err := parseHongguoDanmaku(map[string]any{
		"data": map[string]any{"data_list": []any{
			map[string]any{"comment": map[string]any{
				"comment_id": "c1",
				"common":     map[string]any{"group_id": "7001", "status": "1", "content": map[string]any{"text": "来了"}},
				"expand":     map[string]any{"offset_time": "61000"},
			}},
		}},
	}, "7001", 60_000, 120_000)
	if err == nil {
		t.Fatal("有数据但缺 next 时应当报错")
	}
}

// 预取按 30 秒窗口请求、播放时带真实时长，缓存要能复用（0.8.21）。
func TestHongguoDanmakuCacheIgnoresDuration(t *testing.T) {
	var requests int32
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		atomic.AddInt32(&requests, 1)
		return sourceFixtureResponse(request, http.StatusOK,
			hongguoTestDanmakuBody("7001", "30000", "c1,1000,第一条", "c2,29000,第二条")), nil
	})
	first, err := d.hongguoDanmaku(context.Background(), "6001", "7001", 0, 30_000)
	if err != nil {
		t.Fatalf("首次取弹幕失败：%v", err)
	}
	if len(first.Items) != 2 || first.NextMS != 30_000 {
		t.Fatalf("首次结果不对：%+v", first)
	}
	// 播放时的真实时长与预取不同，但同一窗口应当直接命中缓存。
	second, err := d.hongguoDanmaku(context.Background(), "6001", "7001", 0, 600_000)
	if err != nil {
		t.Fatalf("命中缓存失败：%v", err)
	}
	if len(second.Items) != 2 {
		t.Fatalf("缓存应复用预取结果：%+v", second)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("同一窗口不应重复请求，实际请求 %d 次", got)
	}
}

// 缓存窗口比本次时长更长时要裁掉超出部分（片长短于 30 秒）。
func TestFitDanmakuPageClampsToDuration(t *testing.T) {
	page := hongguoDanmakuPage{
		EpisodeID: "7001", StartMS: 0, NextMS: 30_000,
		Items: []hongguoDanmakuItem{{ID: "a", Text: "早", TimeMS: 1_000}, {ID: "b", Text: "晚", TimeMS: 20_000}},
	}
	clamped, ok := fitDanmakuPage(page, 0, 10_000)
	if !ok {
		t.Fatal("起点一致时应当能复用")
	}
	if clamped.NextMS != 10_000 || len(clamped.Items) != 1 || clamped.Items[0].ID != "a" {
		t.Fatalf("裁剪结果不对：%+v", clamped)
	}
	if len(page.Items) != 2 || page.NextMS != 30_000 {
		t.Fatal("裁剪不能改到原缓存对象")
	}
	if _, ok := fitDanmakuPage(page, 30_000, 60_000); ok {
		t.Fatal("起点不一致时不能复用")
	}
	if _, ok := fitDanmakuPage(page, 0, 0); ok {
		t.Fatal("时长无效时不能复用")
	}
}

// 红果 App 接口回 403 属于签名失配，必须换签名重试（0.8.21）。
func TestHongguoAppRequestRetriesForbidden(t *testing.T) {
	var requests int32
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if atomic.AddInt32(&requests, 1) <= 2 {
			return sourceFixtureResponse(request, http.StatusForbidden, ""), nil
		}
		return sourceFixtureResponse(request, http.StatusOK, `{"code":0,"data":{"ok":true}}`), nil
	})
	result, err := d.hongguoAppRequest(context.Background(), http.MethodPost, "/novel/x/v1/", nil, map[string]any{"a": 1})
	if err != nil {
		t.Fatalf("403 后应重试成功：%v", err)
	}
	if nestedMap(result, "data")["ok"] != true {
		t.Fatalf("返回内容不对：%v", result)
	}
	if got := atomic.LoadInt32(&requests); got < 3 {
		t.Fatalf("应当重试到第 3 次，实际只有 %d 次", got)
	}
}

// 红果 App 接口的 4xx 不能记成 host 级封禁 —— 否则一次偶发 403 就会让
// 详情 / 取流 / 弹幕全部在 acquire 阶段失败十几分钟。
func TestHongguoAppRequestDoesNotSetHostBackoff(t *testing.T) {
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusForbidden, ""), nil
	})
	if _, err := d.hongguoAppRequest(context.Background(), http.MethodPost, "/novel/x/v1/", nil, map[string]any{"a": 1}); err == nil {
		t.Fatal("一直 403 时应当返回错误")
	}
	d.limiter.mu.Lock()
	backoffs := len(d.limiter.backoffs) + len(d.limiter.backgroundBackoffs)
	d.limiter.mu.Unlock()
	if backoffs != 0 {
		t.Fatalf("红果 App 接口不应留下 host 退避，实际 %d 条", backoffs)
	}
}
