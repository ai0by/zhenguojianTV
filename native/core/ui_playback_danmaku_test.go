package core

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

// 有数据但没有 next 字段：按最后一条弹幕的时间戳推窗口边界（0.8.23）。
//
// 以前这里直接报"格式无效"，前端会把这一窗标红并反复退避重试 —— 但缺 next
// 是响应的**结构**问题，重试永远拿不到 next，用户看到的就是"这一集弹幕
// 加载失败"。探针实测：冷门剧整集无弹幕时连 data_list 都不下发，能走到
// "有数据但缺 next"这一步的都是**有弹幕的剧**，报错牺牲的恰恰是正常剧。
func TestParseHongguoDanmakuMissingNextWithRows(t *testing.T) {
	page, err := parseHongguoDanmaku(map[string]any{
		"data": map[string]any{"data_list": []any{
			map[string]any{"comment": map[string]any{
				"comment_id": "c1",
				"common":     map[string]any{"group_id": "7001", "status": "1", "content": map[string]any{"text": "来了"}},
				"expand":     map[string]any{"offset_time": "61000"},
			}},
			map[string]any{"comment": map[string]any{
				"comment_id": "c2",
				"common":     map[string]any{"group_id": "7001", "status": "1", "content": map[string]any{"text": "第二条"}},
				"expand":     map[string]any{"offset_time": "65000"},
			}},
		}},
	}, "7001", 60_000, 120_000)
	if err != nil {
		t.Fatalf("有数据但缺 next 不应报错：%v", err)
	}
	// 窗口边界 = 最后一条弹幕位置 + 1ms，两条都应保留。
	if len(page.Items) != 2 {
		t.Fatalf("两条弹幕都应保留，实际 %d 条", len(page.Items))
	}
	if page.NextMS != 65_001 {
		t.Fatalf("窗口应收在最后一条弹幕 +1ms，实际 %d", page.NextMS)
	}
	// 弹幕时间戳全部无效时按空窗口推进。
	_, err = parseHongguoDanmaku(map[string]any{
		"data": map[string]any{"data_list": []any{
			map[string]any{"comment": map[string]any{
				"comment_id": "c1",
				"common":     map[string]any{"group_id": "7001", "status": "1", "content": map[string]any{"text": "来了"}},
				"expand":     map[string]any{"offset_time": "not-a-number"},
			}},
		}},
	}, "7001", 60_000, 120_000)
	if err != nil {
		t.Fatalf("时间戳无效应按空窗口推进：%v", err)
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

// 0.8.23：空正文之后的退避必须跨出服务端失败窗（约 0.5~1.5 秒）。
//
// 探针实测：空正文失败不是独立事件，同一时间片内的请求共享命运（并发 2 份
// 对冲成功率反而低于单发）。150~400ms 的快速连发全落在同一个失败窗里，
// 救不回来还可能加重风控。这里验证退避序列的下界不小于 0.5 秒。
func TestHongguoAppRetryDelayEscapesFailureWindow(t *testing.T) {
	for step := 1; step <= 6; step++ {
		for i := 0; i < 20; i++ {
			if got := hongguoAppRetryDelay(step); got < 500*time.Millisecond {
				t.Fatalf("第 %d 档退避 %s 仍在失败窗内（<0.5s）", step, got)
			}
		}
	}
	// 序列应递增到封顶：前几档的平均值要低于后几档（抖动 ±20% 下成立）。
	first := hongguoAppRetryDelay(1)
	last := hongguoAppRetryDelay(6)
	if first > last {
		t.Fatalf("退避序列应递增：第 1 档 %s > 第 6 档 %s", first, last)
	}
}

// 0.8.23：请求始终签名。探针实测裸请求（n=20）全部返回空正文——
// 签名头是服务端的准入条件，"最后两次退回裸请求"只是把预算扔进必然失败。
func TestHongguoAppRequestAlwaysSigns(t *testing.T) {
	original := append([]time.Duration(nil), hongguoAppRetryDelays...)
	t.Cleanup(func() { hongguoAppRetryDelays = original })
	hongguoAppRetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond}
	var signed, total int32
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		atomic.AddInt32(&total, 1)
		if request.Header.Get("X-Gorgon") != "" {
			atomic.AddInt32(&signed, 1)
		}
		// 全程空正文 → 跑满注册设备的 5 次 emptyBudget。
		return sourceFixtureResponse(request, http.StatusOK, ""), nil
	})
	if _, err := d.hongguoAppRequest(context.Background(), http.MethodPost, "/novel/x/v1/", nil, map[string]any{"a": 1}); err == nil {
		t.Fatal("一直空正文时应当返回错误")
	}
	got := atomic.LoadInt32(&total)
	if got != 5 {
		t.Fatalf("注册设备应跑满 5 次 emptyBudget，实际 %d 次", got)
	}
	if signedCount := atomic.LoadInt32(&signed); signedCount != got {
		t.Fatalf("全部 %d 次请求都应带签名，实际 %d 次", got, signedCount)
	}
}

// 空正文的整轮重试要在预算内跑完（跨窗退避 0.8+1.4+2.0+2.5s ≈ 6.7s，
// 外层普通预算 25s），不能因为退避变长而把预算吃穿。这里把退避表换成
// 真实序列、但把抖动算进去做**算术校验**，不真等。
func TestHongguoAppRequestEmptyRetryWithinBudget(t *testing.T) {
	original := append([]time.Duration(nil), hongguoAppRetryDelays...)
	t.Cleanup(func() { hongguoAppRetryDelays = original })
	var total time.Duration
	for step := 1; step <= 5; step++ {
		// 抖动 ±20%：取 +20% 上界。
		total += hongguoAppRetryDelay(step) * 6 / 5
	}
	if total > 15*time.Second {
		t.Fatalf("5 次空正文重试的累计退避上界 %s 超预算", total)
	}
	// 用毫秒级退避真跑一遍整轮，验证计数与错误路径。
	hongguoAppRetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond}
	var calls int32
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return sourceFixtureResponse(request, http.StatusOK, ""), nil
	})
	if _, err := d.hongguoAppRequest(context.Background(), http.MethodPost, "/novel/x/v1/", nil, map[string]any{"a": 1}); err == nil {
		t.Fatal("一直空正文时应当返回错误")
	}
	if got := atomic.LoadInt32(&calls); got != 5 {
		t.Fatalf("注册设备应跑满 5 次 emptyBudget，实际 %d 次", got)
	}
}
