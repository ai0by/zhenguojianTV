package core

// 红果 v5 取流的**生产链路**实地验收（2026-11-19）。
//
// 与 hgv5/live_test.go 的区别：这里刻意走生产 Downloader 的完整发包路径
// （doPreparedCatalogRequest → 真实 transport / limiter / CDN / 代理兜底），
// 而不是 openhongguo 原仓那种裸 http.Client。目的是验证「v5 适配层接进
// 生产 core 之后真的能出片」，避免只验证了协议层、集成层却断了。
//
// 默认跳过；要跑：
//
//	HONGGUO_LIVE_PROBE=1 go test ./core -run TestHongguoV5LiveThroughProduction -v -count=1

import (
	"context"
	"encoding/hex"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestHongguoV5LiveThroughProduction 用生产 Downloader 走一遍 v5 取流 + 解析，
// 断言拿到可播地址与非空 CENC key，且不是全 bytevc2（片源盒 hvc1 可解码）。
func TestHongguoV5LiveThroughProduction(t *testing.T) {
	requireProbe(t)
	d := probeDownloader(t)

	// 复刻 resolveHongguoAppMedia 给 v5 的 ctx 标记：红果签名失配不计 host 封禁、
	// 播放取流最高优先级。这样测试跑的 ctx 与生产完全一致。
	ctx := context.WithValue(context.Background(), skipCatalogObserveKey{}, true)
	ctx = withPlaybackPriority(ctx)
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	// 2026-11-19 真网验证可播的一集（hgv5/live_test.go 同款）。
	const videoID = "7697465535398677529"

	model, err := d.hongguoV5PlaybackModel(ctx, videoID)
	if err != nil {
		t.Fatalf("生产链路 v5 取流失败：%v", err)
	}
	if len(model) == 0 {
		t.Fatal("生产链路 v5 返回空 model")
	}
	if hongguoModelNeedsCodecRescue(model) {
		t.Fatal("v5 取到的 model 全档 bytevc2（不可播），与真网验证结论不符")
	}

	media, err := selectHongguoAppVideoList(model)
	if err != nil {
		t.Fatalf("v5 model 无法解析出可播地址：%v", err)
	}
	if !strings.HasPrefix(media.URL, "http") {
		t.Fatalf("可播地址异常：%q", media.URL)
	}
	if len(media.CENCKey) != 16 {
		t.Fatalf("CENC key 应为 16 字节，实际 %d", len(media.CENCKey))
	}
	t.Logf("OK: 生产链路 v5 取流成功，URL=%s codec-rescue-needed=%v CENCKey=%s",
		media.URL, hongguoModelNeedsCodecRescue(model), hex.EncodeToString(media.CENCKey))
}

// TestHongguoV5LiveResolveEntry 走 App 真正调用的生产入口 resolveHongguoAppMedia，
// 验证「v5 优先 → 现有解析 → 试看探测 → 缓存 → singleflight」这条完整链路出片。
// 只有 v5 命中、不回退 v4 才算通过（回退会记 hongguo_v5_playback_fallback 诊断）。
func TestHongguoV5LiveResolveEntry(t *testing.T) {
	requireProbe(t)
	d := probeDownloader(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const videoID = "7697465535398677529"
	media, err := d.resolveHongguoAppMedia(ctx, videoID)
	if err != nil {
		t.Fatalf("生产入口解析失败：%v", err)
	}
	if !strings.HasPrefix(media.URL, "http") {
		t.Fatalf("可播地址异常：%q", media.URL)
	}
	if len(media.CENCKey) != 16 {
		t.Fatalf("CENC key 应为 16 字节，实际 %d", len(media.CENCKey))
	}
	// 第二发应命中 10 分钟短缓存、不再发任何网络请求。
	cached, err := d.resolveHongguoAppMedia(ctx, videoID)
	if err != nil || cached.URL != media.URL {
		t.Fatalf("短缓存未命中：err=%v cached=%q", err, cached.URL)
	}
	t.Logf("OK: 完整生产入口出片 + 短缓存命中，Duration=%s CENCKey=%s",
		media.Duration, hex.EncodeToString(media.CENCKey))
}

// probeCollectHongguoVideoIDs 从真实分类接口 + 详情接口收集 want 个不重复的
// 视频 ID（跨多部剧，覆盖不同剧集），用于并发批量取流验收。
func probeCollectHongguoVideoIDs(t *testing.T, d *Downloader, want int) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	payload := map[string]any{
		"req_scene": "default", "offset": 0, "limit": 18,
		"req_type": "only_content", "need_selector_panel": false, "client_req_type": 3,
		"session_id": "", "filter_ids": "",
		"select_items": map[string]any{
			"genre": []string{"short_play"}, "sort": []string{"online_time"}, "gender": []string{},
			"category_dim_theme": []string{}, "category_dim_role": []string{}, "category_dim_epoch": []string{},
			"online_time": []string{}, "creation_status": []string{},
		},
	}
	var seriesIDs []string
	for attempt := 0; attempt < 5 && len(seriesIDs) == 0; attempt++ {
		result, err := d.hongguoAppRequest(ctx, http.MethodPost, "/reading/distribution/category/landpage/v/", nil, payload)
		if err != nil {
			t.Logf("[并发100] 分类接口失败（第 %d 次）：%v", attempt+1, err)
			time.Sleep(time.Second)
			continue
		}
		rows, _ := nestedMap(result, "data")["video_data"].([]any)
		seen := map[string]bool{}
		for _, row := range rows {
			drama := hongguoDramaFromAny(row, "short_play")
			if drama.ID == "" {
				continue
			}
			_, seriesID, _ := splitProviderDramaID(drama.ID)
			if seriesID == "" || seen[seriesID] {
				continue
			}
			seen[seriesID] = true
			seriesIDs = append(seriesIDs, seriesID)
		}
	}
	if len(seriesIDs) == 0 {
		t.Fatal("拿不到红果剧集列表，无法做并发取流验收")
	}

	ids := make([]string, 0, want)
	seenID := map[string]bool{}
	for _, seriesID := range seriesIDs {
		entry, err := d.hongguoAppDetail(ctx, seriesID)
		if err != nil || len(entry.Chapters) == 0 {
			t.Logf("[并发100] 详情失败 series=%s err=%v", seriesID, err)
			continue
		}
		for _, chapter := range entry.Chapters {
			id := strings.TrimPrefix(chapter.VideoURL, "hongguo-cenc://")
			if !hongguoNumericID.MatchString(id) || seenID[id] {
				continue
			}
			seenID[id] = true
			ids = append(ids, id)
			if len(ids) == want {
				return ids
			}
		}
	}
	t.Fatalf("只收集到 %d/%d 个可用视频 ID（遍历 %d 部剧）", len(ids), want, len(seriesIDs))
	return ids
}

// TestHongguoV5Concurrent100Episodes 是用户要求的「并发取 100 集」验收。
//
// 与前面两条单集测试不同，这里刻意并发打满生产入口 resolveHongguoAppMedia，
// 覆盖 100 个真实、互不相同的剧集 ID，检验：
//   - 每台匿名设备只放行一次的限制下，4 轮换设备是否足够；
//   - limiter（生产 3 并发）/ singleflight / 短缓存 / diagnostics 在并发下是否安全；
//   - 100 集是否都能拿到 http 可播地址 + 16 字节 CENC key。
//
// 默认跳过；要跑：
//
//	HONGGUO_LIVE_PROBE=1 go test ./core -run TestHongguoV5Concurrent100Episodes -v -count=1
//	并发度可用 HONGGUO_PROBE_CONCURRENCY 覆盖（默认 12）。
func TestHongguoV5Concurrent100Episodes(t *testing.T) {
	requireProbe(t)
	d := probeDownloader(t)

	const total = 100
	ids := probeCollectHongguoVideoIDs(t, d, total)

	parallel := 12
	if raw := os.Getenv("HONGGUO_PROBE_CONCURRENCY"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 && value <= 100 {
			parallel = value
		}
	}

	type result struct {
		id      string
		err     error
		url     string
		key     string
		elapsed time.Duration
	}
	results := make([]result, len(ids))
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	started := time.Now()

	for index, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(index int, id string) {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			begin := time.Now()
			media, err := d.resolveHongguoAppMedia(ctx, id)
			results[index] = result{id: id, err: err, url: media.URL,
				key: hex.EncodeToString(media.CENCKey), elapsed: time.Since(begin)}
		}(index, id)
	}
	wg.Wait()

	var failures []result
	elapsed := make([]time.Duration, 0, len(results))
	for _, item := range results {
		elapsed = append(elapsed, item.elapsed)
		if item.err != nil || !strings.HasPrefix(item.url, "http") || len(item.key) != 32 {
			failures = append(failures, item)
		}
	}
	sort.Slice(elapsed, func(i, j int) bool { return elapsed[i] < elapsed[j] })
	sum := time.Duration(0)
	for _, value := range elapsed {
		sum += value
	}
	t.Logf("[并发100] 总数=%d 成功=%d 失败=%d 并发=%d 总耗时=%s 平均=%s p50=%s p95=%s max=%s",
		len(results), len(results)-len(failures), len(failures), parallel, time.Since(started).Round(time.Millisecond),
		(sum / time.Duration(len(elapsed))).Round(time.Millisecond),
		elapsed[len(elapsed)/2].Round(time.Millisecond),
		elapsed[len(elapsed)*95/100].Round(time.Millisecond),
		elapsed[len(elapsed)-1].Round(time.Millisecond))
	for _, item := range failures[:min(len(failures), 10)] {
		t.Logf("[并发100] 失败 id=%s err=%v url=%q key=%q", item.id, item.err, item.url, item.key)
	}
	if len(failures) > 0 {
		t.Fatalf("并发 100 集有 %d 集取流失败", len(failures))
	}
}
