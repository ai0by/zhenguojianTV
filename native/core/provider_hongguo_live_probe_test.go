package core

// 红果 App 接口的**实地探针**（0.8.23 分析用，不影响实现）。
//
// 目的：回答"为什么会有失败"——不是靠推断，而是把同一套签名/请求原样发
// 若干次，统计单发成功率、失败形态（403 / 空正文 / 业务码 / 无 model）以及
// 弹幕接口的空数据比例。
//
// 默认跳过；要跑：
//
//	HONGGUO_LIVE_PROBE=1 go test ./core -run TestHongguoLiveProbe -v -count=1
//
// 可选环境变量：
//
//	HONGGUO_PROBE_ROUNDS  每类请求的次数（默认 30）
//	HONGGUO_PROBE_PROXY   设为 1 时走系统代理（默认强制直连）
//	HONGGUO_PROBE_DUMP    设为 1 时打印失败样本的正文前缀
//
// 注意：这里刻意**不走** `hongguoAppRequest` 的内部重试 —— 否则统计到的是
// "重试之后"的结果，看不到单发失败率，而单发失败率才是用户体感的来源。

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type probeOutcome struct {
	status  int
	bodyLen int
	code    string
	err     error
	body    []byte
}

func (o probeOutcome) label() string {
	switch {
	case o.err != nil:
		return "transport"
	case o.status == http.StatusForbidden:
		return "403"
	case o.status == http.StatusTooManyRequests:
		return "429"
	case o.status >= 400 && o.status < 500:
		return "4xx"
	case o.status >= 500:
		return "5xx"
	case o.bodyLen == 0:
		return "empty"
	case o.code != "" && o.code != "0":
		return "code"
	default:
		return "ok"
	}
}

type probeTally struct {
	name   string
	counts map[string]int
	total  int
	// 最长连续失败（不含 ok）
	streak    int
	best      int
	durations []time.Duration
}

func newTally(name string) *probeTally {
	return &probeTally{name: name, counts: map[string]int{}}
}

func (t *probeTally) add(o probeOutcome, elapsed time.Duration) {
	label := o.label()
	t.total++
	t.counts[label]++
	t.durations = append(t.durations, elapsed)
	if label == "ok" {
		t.streak = 0
		return
	}
	t.streak++
	if t.streak > t.best {
		t.best = t.streak
	}
}

func (t *probeTally) String() string {
	keys := []string{"ok", "empty", "code", "403", "429", "4xx", "5xx", "transport"}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if value := t.counts[key]; value > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", key, value))
		}
	}
	okRate := 0.0
	if t.total > 0 {
		okRate = float64(t.counts["ok"]) * 100 / float64(t.total)
	}
	avg, max := time.Duration(0), time.Duration(0)
	for _, d := range t.durations {
		avg += d
		if d > max {
			max = d
		}
	}
	if len(t.durations) > 0 {
		avg /= time.Duration(len(t.durations))
	}
	return fmt.Sprintf("%-28s n=%-3d ok=%.1f%% [%s] avg=%s max=%s worst-streak=%d",
		t.name, t.total, okRate, strings.Join(parts, " "), avg.Round(time.Millisecond), max.Round(time.Millisecond), t.best)
}

// probeDownloader 构造一个尽量接近真机的 Downloader（同一套 Transport 与
// 代理路由），但不注册任何测试替身。
func probeDownloader(t *testing.T) *Downloader {
	t.Helper()
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.dataDir = dir
	router := &proxyRouter{}
	d := &Downloader{
		cfg: cfg, providerHosts: map[string]string{}, proxyRouter: router,
		limiter: newRequestLimiter(3, 250*time.Millisecond), diagnostics: newDiagnosticLog(dir),
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.MaxIdleConnsPerHost = 8
	transport.ResponseHeaderTimeout = 20 * time.Second
	transport.DialContext = newForceableDialer()
	if os.Getenv("HONGGUO_PROBE_PROXY") != "1" {
		transport.Proxy = nil
	} else {
		transport.Proxy = router.proxy
	}
	cdn := newCDNTransport(transport, newDNSResolver(transport))
	d.client = &http.Client{Transport: newHuangguoBrowserTransport(cdn, d), Timeout: 45 * time.Second}
	return d
}

func probeRounds(t *testing.T) int {
	t.Helper()
	if raw := os.Getenv("HONGGUO_PROBE_ROUNDS"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 && value <= 500 {
			return value
		}
	}
	return 30
}

// probeOnce 发**一发**原始请求（不做任何重试），签名与真实链路一致。
func probeOnce(ctx context.Context, d *Downloader, method, path string, extra url.Values, payload any, signed, comment bool) probeOutcome {
	client := d.hongguoClient()
	client.mu.Lock()
	base, deviceID, installID := client.baseURL, client.state.DeviceID, client.state.InstallID
	client.mu.Unlock()
	query := url.Values{
		"aid": {"8662"}, "app_name": {"novelread"}, "version_code": {hongguoAppVersionCode}, "version_name": {hongguoAppVersionName},
		"manifest_version_code": {hongguoAppVersionCode}, "update_version_code": {hongguoAppVersionCode}, "channel": {"update_64"},
		"device_platform": {"android"}, "os": {"android"}, "ssmix": {"a"}, "device_type": {"25053RT47C"},
		"device_brand": {"Redmi"}, "language": {"zh"}, "os_api": {"36"}, "os_version": {"16"},
		"resolution": {"1280*2772"}, "dpi": {"520"}, "ac": {"wifi"}, "device_id": {deviceID}, "iid": {installID},
	}
	for key, values := range extra {
		query[key] = append([]string(nil), values...)
	}
	var body []byte
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return probeOutcome{err: err}
		}
		body = encoded
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, bytes.NewReader(body))
	if err != nil {
		return probeOutcome{err: err}
	}
	request.Header.Set("User-Agent", hongguoAppUserAgent)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-XS-From-Web", "0")
	request.Header.Set("Sdk-Version", "2")
	var nonce hongguoCommentNonce
	if comment {
		if nonce, err = newHongguoCommentNonce(); err != nil {
			return probeOutcome{err: err}
		}
		request.Header.Set("Comment-Source", "601")
		request.Header.Set("Server-Channel", "1000")
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	response, err := d.doPreparedCatalogRequest(request, 20*time.Second, func(prepared *http.Request) {
		now := time.Now()
		query.Set("_rticket", strconv.FormatInt(now.UnixMilli(), 10))
		prepared.URL.RawQuery = query.Encode()
		if comment {
			signHongguoCommentRequest(prepared, nonce, now)
		} else if signed {
			signHongguoAppRequest(prepared, body, prepared.URL.RawQuery, deviceID, now)
		}
	})
	if err != nil {
		return probeOutcome{err: err}
	}
	content, readErr := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
	response.Body.Close()
	if readErr != nil {
		return probeOutcome{status: response.StatusCode, err: readErr}
	}
	outcome := probeOutcome{status: response.StatusCode, bodyLen: len(content), body: content}
	if response.StatusCode == http.StatusOK && len(content) > 0 {
		var result map[string]any
		decoder := json.NewDecoder(bytes.NewReader(content))
		decoder.UseNumber()
		if decoder.Decode(&result) == nil && result != nil {
			outcome.code = firstNonEmpty(mapString(result, "code", "Code", "status_code"), mapString(nestedMap(result, "BaseResp"), "StatusCode"))
		}
	}
	return outcome
}

// probeCatalogSeries 用真实分类接口取一部剧的 series_id 和第一集的 video_id。
func probeCatalogSeries(t *testing.T, d *Downloader) (string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	var seriesID string
	for attempt := 0; attempt < 5 && seriesID == ""; attempt++ {
		result, err := d.hongguoAppRequest(ctx, http.MethodPost, "/reading/distribution/category/landpage/v/", nil, payload)
		if err != nil {
			t.Logf("[probe] 分类接口失败（第 %d 次）：%v", attempt+1, err)
			time.Sleep(time.Second)
			continue
		}
		rows, _ := nestedMap(result, "data")["video_data"].([]any)
		for _, row := range rows {
			drama := hongguoDramaFromAny(row, "short_play")
			if drama.ID != "" {
				_, sourceID, _ := splitProviderDramaID(drama.ID)
				seriesID = sourceID
				break
			}
		}
	}
	if seriesID == "" {
		t.Skip("拿不到可用的红果剧集，跳过实地探针")
	}
	// 详情拿第一集的视频 ID（走真实解析，结构以 parseHongguoAppDetail 为准）。
	var videoID string
	for attempt := 0; attempt < 5 && videoID == ""; attempt++ {
		entry, err := d.hongguoAppDetail(ctx, seriesID)
		if err != nil {
			t.Logf("[probe] 详情接口失败（第 %d 次）：%v", attempt+1, err)
			time.Sleep(time.Second)
			continue
		}
		if len(entry.Chapters) > 0 {
			videoID = strings.TrimPrefix(entry.Chapters[0].VideoURL, "hongguo-cenc://")
		}
		if os.Getenv("HONGGUO_PROBE_DUMP") == "1" {
			t.Logf("[probe] 详情：剧名=%q 集数=%d 首集=%s", entry.Drama.Title, len(entry.Chapters), videoID)
		}
	}
	if videoID == "" {
		t.Skip("拿不到可用的红果视频 ID，跳过实地探针")
	}
	t.Logf("[probe] 样本：series=%s video=%s", seriesID, videoID)
	return seriesID, videoID
}

func requireProbe(t *testing.T) {
	t.Helper()
	if os.Getenv("HONGGUO_LIVE_PROBE") != "1" {
		t.Skip("设置 HONGGUO_LIVE_PROBE=1 才跑实地探针")
	}
}

// TestHongguoLiveProbeSigning 统计**单发**签名的命中率，以及不签名时的对比。
func TestHongguoLiveProbeSigning(t *testing.T) {
	requireProbe(t)
	d := probeDownloader(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := d.ensureHongguoDevice(ctx); err != nil {
		t.Fatalf("设备注册失败：%v", err)
	}
	seriesID, videoID := probeCatalogSeries(t, d)
	payload := map[string]any{"series_id": seriesID}
	rounds := probeRounds(t)

	signed := newTally("video_detail 签名")
	for i := 0; i < rounds; i++ {
		started := time.Now()
		outcome := probeOnce(ctx, d, http.MethodPost, "/novel/player/video_detail/v1/", nil, payload, true, false)
		signed.add(outcome, time.Since(started))
		if outcome.label() != "ok" && os.Getenv("HONGGUO_PROBE_DUMP") == "1" {
			t.Logf("  签名失败样本 status=%d len=%d err=%v body=%q", outcome.status, outcome.bodyLen, outcome.err, truncate(string(outcome.body), 160))
		}
		time.Sleep(120 * time.Millisecond)
	}
	t.Log(signed.String())

	bare := newTally("video_detail 裸请求")
	for i := 0; i < rounds/2; i++ {
		started := time.Now()
		outcome := probeOnce(ctx, d, http.MethodPost, "/novel/player/video_detail/v1/", nil, payload, false, false)
		bare.add(outcome, time.Since(started))
		time.Sleep(120 * time.Millisecond)
	}
	t.Log(bare.String())

	// 取流：单发请求里"有 video_model"的比例。
	media := newTally("multi_video_model 单发")
	emptyModel := 0
	for i := 0; i < rounds/2; i++ {
		started := time.Now()
		outcome := probeOnce(ctx, d, http.MethodPost, "/novel/player/multi_video_model/v1/", hongguoPlayerQuery(), map[string]any{
			"biz_param": map[string]any{
				"detail_page_version": 0, "device_level": 3, "disable_digg_stat": false,
				"need_all_video_definition": true, "need_mp4_align": false, "use_os_player": false,
				"use_server_dns": false, "video_platform": 1024,
			},
			"mixed_video_id_map": map[string]any{"1004": []string{videoID}},
		}, true, false)
		media.add(outcome, time.Since(started))
		if outcome.label() == "ok" {
			var result map[string]any
			decoder := json.NewDecoder(bytes.NewReader(outcome.body))
			decoder.UseNumber()
			if decoder.Decode(&result) == nil && result != nil {
				entry, _ := nestedMap(result, "data")[videoID].(map[string]any)
				model, _ := entry["video_model"].(map[string]any)
				encoded, isString := entry["video_model"].(string)
				if len(model) == 0 && !(isString && len(encoded) > 0) {
					emptyModel++
				}
			}
		}
		time.Sleep(120 * time.Millisecond)
	}
	t.Log(media.String())
	t.Logf("%-28s 有响应但无 video_model=%d / %d", "multi_video_model 单发", emptyModel, rounds/2)
}

// TestHongguoLiveProbeDanmaku 统计弹幕接口的返回形态：空数据、缺 next、
// 同一窗口多次请求的条目数波动。
func TestHongguoLiveProbeDanmaku(t *testing.T) {
	requireProbe(t)
	d := probeDownloader(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := d.ensureHongguoDevice(ctx); err != nil {
		t.Fatalf("设备注册失败：%v", err)
	}
	seriesID, videoID := probeCatalogSeries(t, d)
	rounds := probeRounds(t)
	t.Logf("[probe] 弹幕样本 series=%s video=%s rounds=%d", seriesID, videoID, rounds)

	body := func(start, duration int64) map[string]any {
		return map[string]any{
			"comment_source": 601, "server_channel": 1000, "group_id": videoID, "group_type": 30,
			"comment_type": 20, "sort": 1, "count": 90, "cursor": "", "aid": 8662, "compliance_status": 0,
			"business_param": map[string]any{
				"book_id": seriesID, "start_offset_time": start, "playlet_item_duration": duration,
				"need_danmaku_guide_type": []int{1, 3, 4, 2},
			},
		}
	}

	type sample struct {
		label      string
		status     int
		bodyLen    int
		hasList    bool
		hasNext    bool
		itemCount  int
		nextMS     int64
		errMessage string
	}
	run := func(name string, start, duration int64, n int) {
		tally := newTally(name)
		var samples []sample
		zero, noNext, noList := 0, 0, 0
		var mu sync.Mutex
		for i := 0; i < n; i++ {
			started := time.Now()
			outcome := probeOnce(ctx, d, http.MethodPost, "/novel/commentapi/comment/list/"+videoID+"/v1/", nil, body(start, duration), false, true)
			tally.add(outcome, time.Since(started))
			s := sample{label: name, status: outcome.status, bodyLen: outcome.bodyLen}
			if outcome.err != nil {
				s.errMessage = outcome.err.Error()
			} else if outcome.status == http.StatusOK && outcome.bodyLen > 0 {
				var result map[string]any
				decoder := json.NewDecoder(bytes.NewReader(outcome.body))
				decoder.UseNumber()
				if decoder.Decode(&result) == nil && result != nil {
					data := nestedMap(result, "data")
					rows, ok := data["data_list"].([]any)
					s.hasList = ok
					s.itemCount = len(rows)
					next, err := strconv.ParseInt(mapString(nestedMap(data, "extra"), "next_query_danmaku_list_time"), 10, 64)
					s.hasNext = err == nil && next > 0
					s.nextMS = next
					if !ok {
						noList++
					} else if len(rows) == 0 {
						zero++
					}
					if !s.hasNext {
						noNext++
					}
				}
			}
			mu.Lock()
			samples = append(samples, s)
			mu.Unlock()
			time.Sleep(150 * time.Millisecond)
		}
		t.Log(tally.String())
		t.Logf("%-28s 空列表=%d 缺next=%d 缺data_list=%d", name, zero, noNext, noList)
		// 打印前几个非 ok 的样本，便于判断失败形态。
		shown := 0
		for _, s := range samples {
			if s.status != http.StatusOK || s.bodyLen == 0 {
				if shown < 3 {
					t.Logf("  失败样本 status=%d len=%d err=%q", s.status, s.bodyLen, s.errMessage)
					shown++
				}
			}
		}
	}

	// 第一窗（播放时会请求的窗口）重复 30 次，看服务端是否稳定。
	run("弹幕 0-30s 重复", 0, 30_000, rounds)
	// 同一集、真实时长下的第一窗（预取与播放的键要能对上）。
	run("弹幕 0-90s", 0, 90_000, rounds/2)
	// 中段窗口。
	run("弹幕 60-90s", 60_000, 90_000, rounds/2)
	// 片尾（大概率没人发）。
	run("弹幕 尾部", 600_000, 660_000, rounds/3+1)
}

// TestHongguoLiveProbeDanmakuVariants 用同一部剧试不同参数组合，判断
// "全空"到底是这部剧没弹幕，还是我们的请求参数不对。
func TestHongguoLiveProbeDanmakuVariants(t *testing.T) {
	requireProbe(t)
	d := probeDownloader(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := d.ensureHongguoDevice(ctx); err != nil {
		t.Fatalf("设备注册失败：%v", err)
	}
	seriesID, videoID := probeCatalogSeries(t, d)

	type variant struct {
		name       string
		group      string
		groupType  int
		commentTyp int
		sort       int
		count      int
		bookID     string
	}
	variants := []variant{
		{"当前实现", videoID, 30, 20, 1, 90, seriesID},
		{"group=剧集", seriesID, 30, 20, 1, 90, seriesID},
		{"group_type=0", videoID, 0, 20, 1, 90, seriesID},
		{"group_type=1", videoID, 1, 20, 1, 90, seriesID},
		{"comment_type=0", videoID, 30, 0, 1, 90, seriesID},
		{"sort=0", videoID, 30, 20, 0, 90, seriesID},
		{"count=200", videoID, 30, 20, 1, 200, seriesID},
		{"book_id=视频", videoID, 30, 20, 1, 90, videoID},
	}
	for _, item := range variants {
		body := map[string]any{
			"comment_source": 601, "server_channel": 1000, "group_id": item.group, "group_type": item.groupType,
			"comment_type": item.commentTyp, "sort": item.sort, "count": item.count, "cursor": "", "aid": 8662, "compliance_status": 0,
			"business_param": map[string]any{
				"book_id": item.bookID, "start_offset_time": 0, "playlet_item_duration": 90_000,
				"need_danmaku_guide_type": []int{1, 3, 4, 2},
			},
		}
		outcome := probeOnce(ctx, d, http.MethodPost, "/novel/commentapi/comment/list/"+videoID+"/v1/", nil, body, false, true)
		items, next, total := -1, "", ""
		if outcome.err == nil && outcome.status == http.StatusOK && outcome.bodyLen > 0 {
			var result map[string]any
			decoder := json.NewDecoder(bytes.NewReader(outcome.body))
			decoder.UseNumber()
			if decoder.Decode(&result) == nil && result != nil {
				data := nestedMap(result, "data")
				if rows, ok := data["data_list"].([]any); ok {
					items = len(rows)
				}
				next = mapString(nestedMap(data, "extra"), "next_query_danmaku_list_time")
				total = mapString(data, "total")
			}
		}
		t.Logf("%-18s status=%d len=%-6d items=%-3d next=%-10q total=%q err=%v",
			item.name, outcome.status, outcome.bodyLen, items, next, total, outcome.err)
		time.Sleep(200 * time.Millisecond)
	}
}

// TestHongguoLiveProbeDanmakuAcrossDramas 换若干部剧各取第一集，看"有弹幕"
// 的比例 —— 判断空列表是普遍现象还是个别剧。
func TestHongguoLiveProbeDanmakuAcrossDramas(t *testing.T) {
	requireProbe(t)
	d := probeDownloader(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if err := d.ensureHongguoDevice(ctx); err != nil {
		t.Fatalf("设备注册失败：%v", err)
	}
	// 直接翻分类页拿多部剧。
	series := make([]string, 0, 6)
	for offset := 0; offset < 54 && len(series) < 6; offset += 18 {
		payload := map[string]any{
			"req_scene": "default", "offset": offset, "limit": 18,
			"req_type": "only_content", "need_selector_panel": false, "client_req_type": 3,
			"session_id": "", "filter_ids": "",
			"select_items": map[string]any{
				"genre": []string{"short_play"}, "sort": []string{"online_time"}, "gender": []string{},
				"category_dim_theme": []string{}, "category_dim_role": []string{}, "category_dim_epoch": []string{},
				"online_time": []string{}, "creation_status": []string{},
			},
		}
		result, err := d.hongguoAppRequest(ctx, http.MethodPost, "/reading/distribution/category/landpage/v/", nil, payload)
		if err != nil {
			t.Logf("[probe] 分类第 %d 页失败：%v", offset/18+1, err)
			continue
		}
		for _, row := range anyList(nestedMap(result, "data")["video_data"]) {
			drama := hongguoDramaFromAny(row, "short_play")
			if _, sourceID, ok := splitProviderDramaID(drama.ID); ok && sourceID != "" {
				series = append(series, sourceID)
			}
		}
	}
	t.Logf("[probe] 候选剧集 %d 部", len(series))
	withDanmaku := 0
	for _, seriesID := range series {
		entry, err := d.hongguoAppDetail(ctx, seriesID)
		if err != nil || len(entry.Chapters) == 0 {
			t.Logf("  %s 详情失败：%v", seriesID, err)
			continue
		}
		videoID := strings.TrimPrefix(entry.Chapters[0].VideoURL, "hongguo-cenc://")
		body := map[string]any{
			"comment_source": 601, "server_channel": 1000, "group_id": videoID, "group_type": 30,
			"comment_type": 20, "sort": 1, "count": 90, "cursor": "", "aid": 8662, "compliance_status": 0,
			"business_param": map[string]any{
				"book_id": seriesID, "start_offset_time": 0, "playlet_item_duration": 90_000,
				"need_danmaku_guide_type": []int{1, 3, 4, 2},
			},
		}
		outcome := probeOnce(ctx, d, http.MethodPost, "/novel/commentapi/comment/list/"+videoID+"/v1/", nil, body, false, true)
		items := -1
		if outcome.err == nil && outcome.status == http.StatusOK && outcome.bodyLen > 0 {
			var result map[string]any
			decoder := json.NewDecoder(bytes.NewReader(outcome.body))
			decoder.UseNumber()
			if decoder.Decode(&result) == nil && result != nil {
				if rows, ok := nestedMap(result, "data")["data_list"].([]any); ok {
					items = len(rows)
				}
			}
		}
		if items > 0 {
			withDanmaku++
		}
		t.Logf("  %s %-24s 第一集 %s items=%d", seriesID, truncate(entry.Drama.Title, 24), videoID, items)
		time.Sleep(150 * time.Millisecond)
	}
	t.Logf("[probe] %d/%d 部剧第一集有弹幕", withDanmaku, len(series))
}

// probeOnceFixedRandom 与 probeOnce 相同，但 X-Gorgon 使用**固定随机数**，
// 用来判断空响应到底由"签名随机数"决定，还是服务端按请求/频率随机拒绝。
func probeOnceFixedRandom(ctx context.Context, d *Downloader, path string, payload any, fixed uint32) (probeOutcome, http.Header) {
	client := d.hongguoClient()
	client.mu.Lock()
	base, deviceID, installID := client.baseURL, client.state.DeviceID, client.state.InstallID
	client.mu.Unlock()
	query := url.Values{
		"aid": {"8662"}, "app_name": {"novelread"}, "version_code": {hongguoAppVersionCode}, "version_name": {hongguoAppVersionName},
		"manifest_version_code": {hongguoAppVersionCode}, "update_version_code": {hongguoAppVersionCode}, "channel": {"update_64"},
		"device_platform": {"android"}, "os": {"android"}, "ssmix": {"a"}, "device_type": {"25053RT47C"},
		"device_brand": {"Redmi"}, "language": {"zh"}, "os_api": {"36"}, "os_version": {"16"},
		"resolution": {"1280*2772"}, "dpi": {"520"}, "ac": {"wifi"}, "device_id": {deviceID}, "iid": {installID},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return probeOutcome{err: err}, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+path, bytes.NewReader(body))
	if err != nil {
		return probeOutcome{err: err}, nil
	}
	request.Header.Set("User-Agent", hongguoAppUserAgent)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-XS-From-Web", "0")
	request.Header.Set("Sdk-Version", "2")
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response, err := d.doPreparedCatalogRequest(request, 20*time.Second, func(prepared *http.Request) {
		now := time.Now()
		khronos := uint32(now.Unix())
		query.Set("_rticket", strconv.FormatInt(now.UnixMilli(), 10))
		prepared.URL.RawQuery = query.Encode()
		prepared.Header.Set("X-Khronos", strconv.FormatUint(uint64(khronos), 10))
		prepared.Header.Set("X-Gorgon", hongguoXGorgon(prepared.URL.RawQuery, body, khronos, fixed))
		prepared.Header.Set("X-SS-Req-Ticket", strconv.FormatInt(now.UnixMilli(), 10))
		prepared.Header.Set("X-Tt-Request-Tag", "t=0;n=0")
		prepared.Header.Set("Passport-Sdk-Version", "50561")
		prepared.Header.Set("X-Vc-Bdturing-Sdk-Version", "3.7.2.cn")
		prepared.Header.Set("X-SS-Stub", fmt.Sprintf("%X", hongguoMD5Sum(body)))
		prepared.Header.Set("X-Argus", base64.StdEncoding.EncodeToString(hongguoLe32(khronos)))
		prepared.Header.Set("X-Ladon", base64.StdEncoding.EncodeToString(hongguoBe32(khronos)))
		prepared.Header.Set("X-Helios", hongguoHelios(khronos, hongguoRandomUint32()))
		if medusa := hongguoBuildMedusa(prepared.URL.String(), body, khronos, deviceID, hongguoSignVersionName, newHongguoMedusaRandom()); medusa != "" {
			prepared.Header.Set("X-Medusa", medusa)
		}
	})
	if err != nil {
		return probeOutcome{err: err}, nil
	}
	content, readErr := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
	headers := response.Header.Clone()
	response.Body.Close()
	if readErr != nil {
		return probeOutcome{status: response.StatusCode, err: readErr}, headers
	}
	return probeOutcome{status: response.StatusCode, bodyLen: len(content), body: content}, headers
}

// TestHongguoLiveProbeSignRandom 固定/扫描随机数，判断空响应是否由签名随机数决定。
func TestHongguoLiveProbeSignRandom(t *testing.T) {
	requireProbe(t)
	d := probeDownloader(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if err := d.ensureHongguoDevice(ctx); err != nil {
		t.Fatalf("设备注册失败：%v", err)
	}
	seriesID, _ := probeCatalogSeries(t, d)
	payload := map[string]any{"series_id": seriesID}

	// 同一随机数连续 8 次：如果结果稳定，说明成败由随机数决定。
	fixed := uint32(0x12345678)
	same := newTally("固定随机数 x8")
	for i := 0; i < 8; i++ {
		started := time.Now()
		outcome, headers := probeOnceFixedRandom(ctx, d, "/novel/player/video_detail/v1/", payload, fixed)
		same.add(outcome, time.Since(started))
		if outcome.bodyLen == 0 {
			t.Logf("  空响应头：logid=%q server=%q ct=%q len=%q",
				headers.Get("X-Tt-Logid"), headers.Get("Server"), headers.Get("Content-Type"), headers.Get("Content-Length"))
		}
		time.Sleep(120 * time.Millisecond)
	}
	t.Log(same.String())

	// 逐个扫描随机数，观察成功率是否随值变化。
	scan := newTally("随机数扫描")
	failValues := []uint32{}
	for i := uint32(0); i < 16; i++ {
		started := time.Now()
		outcome, _ := probeOnceFixedRandom(ctx, d, "/novel/player/video_detail/v1/", payload, i*2654435761)
		scan.add(outcome, time.Since(started))
		if outcome.bodyLen == 0 {
			failValues = append(failValues, i*2654435761)
		}
		time.Sleep(120 * time.Millisecond)
	}
	t.Log(scan.String())
	t.Logf("空响应随机数：%v", failValues)
}

// probeHeaderSet 在真实签名的基础上按需摘掉某个签名字段，用来定位
// "到底哪个头让服务端回空正文"。
func probeHeaderSet(ctx context.Context, d *Downloader, path string, payload any, dropHelios, dropMedusa bool) probeOutcome {
	client := d.hongguoClient()
	client.mu.Lock()
	base, deviceID, installID := client.baseURL, client.state.DeviceID, client.state.InstallID
	client.mu.Unlock()
	query := url.Values{
		"aid": {"8662"}, "app_name": {"novelread"}, "version_code": {hongguoAppVersionCode}, "version_name": {hongguoAppVersionName},
		"manifest_version_code": {hongguoAppVersionCode}, "update_version_code": {hongguoAppVersionCode}, "channel": {"update_64"},
		"device_platform": {"android"}, "os": {"android"}, "ssmix": {"a"}, "device_type": {"25053RT47C"},
		"device_brand": {"Redmi"}, "language": {"zh"}, "os_api": {"36"}, "os_version": {"16"},
		"resolution": {"1280*2772"}, "dpi": {"520"}, "ac": {"wifi"}, "device_id": {deviceID}, "iid": {installID},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return probeOutcome{err: err}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+path, bytes.NewReader(body))
	if err != nil {
		return probeOutcome{err: err}
	}
	request.Header.Set("User-Agent", hongguoAppUserAgent)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-XS-From-Web", "0")
	request.Header.Set("Sdk-Version", "2")
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response, err := d.doPreparedCatalogRequest(request, 20*time.Second, func(prepared *http.Request) {
		now := time.Now()
		query.Set("_rticket", strconv.FormatInt(now.UnixMilli(), 10))
		prepared.URL.RawQuery = query.Encode()
		signHongguoAppRequest(prepared, body, prepared.URL.RawQuery, deviceID, now)
		if dropHelios {
			prepared.Header.Del("X-Helios")
		}
		if dropMedusa {
			prepared.Header.Del("X-Medusa")
		}
	})
	if err != nil {
		return probeOutcome{err: err}
	}
	content, readErr := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
	response.Body.Close()
	if readErr != nil {
		return probeOutcome{status: response.StatusCode, err: readErr}
	}
	return probeOutcome{status: response.StatusCode, bodyLen: len(content), body: content}
}

// TestHongguoLiveProbeHeaderSet 逐个头排除，定位空正文的来源。
func TestHongguoLiveProbeHeaderSet(t *testing.T) {
	requireProbe(t)
	d := probeDownloader(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if err := d.ensureHongguoDevice(ctx); err != nil {
		t.Fatalf("设备注册失败：%v", err)
	}
	seriesID, _ := probeCatalogSeries(t, d)
	payload := map[string]any{"series_id": seriesID}
	cases := []struct {
		name       string
		dropHelios bool
		dropMedusa bool
	}{
		{"全套（当前实现）", false, false},
		{"去掉 X-Medusa", false, true},
		{"去掉 X-Helios", true, false},
		{"两个都去掉", true, true},
	}
	rounds := 12
	for _, item := range cases {
		tally := newTally(item.name)
		for i := 0; i < rounds; i++ {
			started := time.Now()
			tally.add(probeHeaderSet(ctx, d, "/novel/player/video_detail/v1/", payload, item.dropHelios, item.dropMedusa), time.Since(started))
			time.Sleep(120 * time.Millisecond)
		}
		t.Log(tally.String())
	}
}

// probeFixedSignatureRandom 固定 X-Helios / X-Medusa 的随机输入，用来判断
// "同一套签名参数是否会稳定成功/失败"。
func probeFixedSignatureRandom(ctx context.Context, d *Downloader, path string, payload any, fixHelios, fixMedusa bool, seed uint32) probeOutcome {
	client := d.hongguoClient()
	client.mu.Lock()
	base, deviceID, installID := client.baseURL, client.state.DeviceID, client.state.InstallID
	client.mu.Unlock()
	query := url.Values{
		"aid": {"8662"}, "app_name": {"novelread"}, "version_code": {hongguoAppVersionCode}, "version_name": {hongguoAppVersionName},
		"manifest_version_code": {hongguoAppVersionCode}, "update_version_code": {hongguoAppVersionCode}, "channel": {"update_64"},
		"device_platform": {"android"}, "os": {"android"}, "ssmix": {"a"}, "device_type": {"25053RT47C"},
		"device_brand": {"Redmi"}, "language": {"zh"}, "os_api": {"36"}, "os_version": {"16"},
		"resolution": {"1280*2772"}, "dpi": {"520"}, "ac": {"wifi"}, "device_id": {deviceID}, "iid": {installID},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return probeOutcome{err: err}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+path, bytes.NewReader(body))
	if err != nil {
		return probeOutcome{err: err}
	}
	request.Header.Set("User-Agent", hongguoAppUserAgent)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-XS-From-Web", "0")
	request.Header.Set("Sdk-Version", "2")
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response, err := d.doPreparedCatalogRequest(request, 20*time.Second, func(prepared *http.Request) {
		now := time.Now()
		khronos := uint32(now.Unix())
		query.Set("_rticket", strconv.FormatInt(now.UnixMilli(), 10))
		prepared.URL.RawQuery = query.Encode()
		signHongguoAppRequest(prepared, body, prepared.URL.RawQuery, deviceID, now)
		if fixHelios {
			prepared.Header.Set("X-Helios", hongguoHelios(khronos, seed))
		}
		if fixMedusa {
			random := hongguoMedusaRandom{
				MessageRand: seed, EnvLaunch: 110, EnvPid: 12345, Random: seed ^ 0x9e3779b9,
				XmRand: seed ^ 0x85ebca6b, Nonce: hongguoProcessNonce, Started: hongguoProcessStarted, Now: now.Unix(),
			}
			if medusa := hongguoBuildMedusa(prepared.URL.String(), body, khronos, deviceID, hongguoSignVersionName, random); medusa != "" {
				prepared.Header.Set("X-Medusa", medusa)
			}
		}
	})
	if err != nil {
		return probeOutcome{err: err}
	}
	content, readErr := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
	response.Body.Close()
	if readErr != nil {
		return probeOutcome{status: response.StatusCode, err: readErr}
	}
	return probeOutcome{status: response.StatusCode, bodyLen: len(content), body: content}
}

// TestHongguoLiveProbeSignatureRandom 锁定签名里"每次变化"的部分。
func TestHongguoLiveProbeSignatureRandom(t *testing.T) {
	requireProbe(t)
	d := probeDownloader(t)
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	if err := d.ensureHongguoDevice(ctx); err != nil {
		t.Fatalf("设备注册失败：%v", err)
	}
	seriesID, _ := probeCatalogSeries(t, d)
	payload := map[string]any{"series_id": seriesID}

	run := func(name string, fixHelios, fixMedusa bool, seed uint32, n int) {
		tally := newTally(name)
		for i := 0; i < n; i++ {
			started := time.Now()
			tally.add(probeFixedSignatureRandom(ctx, d, "/novel/player/video_detail/v1/", payload, fixHelios, fixMedusa, seed), time.Since(started))
			time.Sleep(120 * time.Millisecond)
		}
		t.Log(tally.String())
	}
	run("固定 Helios 随机数", true, false, 0x12345678, 10)
	run("固定 Helios+Medusa 随机", true, true, 0x12345678, 10)
	run("只固定 Medusa 随机", false, true, 0x12345678, 10)
}

// TestHongguoLiveProbeSecondPhase 观察成功率是否随时间（秒）呈周期性 ——
// 若失败集中在特定秒值，说明是签名里的时间字段，而不是服务端随机丢弃。
func TestHongguoLiveProbeSecondPhase(t *testing.T) {
	requireProbe(t)
	d := probeDownloader(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	if err := d.ensureHongguoDevice(ctx); err != nil {
		t.Fatalf("设备注册失败：%v", err)
	}
	seriesID, _ := probeCatalogSeries(t, d)
	payload := map[string]any{"series_id": seriesID}

	type bucket struct{ ok, total int }
	bySecond := map[int64]*bucket{}
	tally := newTally("秒相位扫描")
	for i := 0; i < 120; i++ {
		started := time.Now()
		second := time.Now().Unix()
		outcome := probeHeaderSet(ctx, d, "/novel/player/video_detail/v1/", payload, false, false)
		tally.add(outcome, time.Since(started))
		entry := bySecond[second]
		if entry == nil {
			entry = &bucket{}
			bySecond[second] = entry
		}
		entry.total++
		if outcome.label() == "ok" {
			entry.ok++
		}
		time.Sleep(180 * time.Millisecond)
	}
	t.Log(tally.String())
	// 只打印至少 2 次的秒，看同一秒内是否结果一致。
	consistent, split := 0, 0
	for second, entry := range bySecond {
		if entry.total < 2 {
			continue
		}
		if entry.ok == 0 || entry.ok == entry.total {
			consistent++
		} else {
			split++
			t.Logf("  秒 %d 内混合：%d/%d 成功", second, entry.ok, entry.total)
		}
	}
	t.Logf("同一秒内结果一致 %d 个、混合 %d 个", consistent, split)
}

// TestHongguoLiveProbeHedged 量化"并发对冲"能带来多少收益：
// 同一发请求并发 2/3 份，任一成功即算成功。
func TestHongguoLiveProbeHedged(t *testing.T) {
	requireProbe(t)
	d := probeDownloader(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	if err := d.ensureHongguoDevice(ctx); err != nil {
		t.Fatalf("设备注册失败：%v", err)
	}
	seriesID, _ := probeCatalogSeries(t, d)
	payload := map[string]any{"series_id": seriesID}

	run := func(copies int, rounds int) (float64, time.Duration) {
		okCount := 0
		var total time.Duration
		for i := 0; i < rounds; i++ {
			started := time.Now()
			winner := make(chan bool, copies)
			for copyIndex := 0; copyIndex < copies; copyIndex++ {
				go func() {
					// 绕过全局并发闸门，否则三份会被串行化，测不出对冲收益。
					probeCtx := context.WithValue(ctx, skipCatalogGateKey{}, true)
					outcome := probeHeaderSet(probeCtx, d, "/novel/player/video_detail/v1/", payload, false, false)
					winner <- outcome.label() == "ok"
				}()
			}
			got := false
			for copyIndex := 0; copyIndex < copies; copyIndex++ {
				if <-winner {
					got = true
					break
				}
			}
			// 剩余结果丢弃（goroutine 会自己结束）。
			if got {
				okCount++
			}
			total += time.Since(started)
			time.Sleep(150 * time.Millisecond)
		}
		return float64(okCount) * 100 / float64(rounds), total / time.Duration(rounds)
	}

	rounds := 16
	rate, avg := run(1, rounds)
	t.Logf("单发       成功 %.1f%% 平均 %s", rate, avg.Round(time.Millisecond))
	rate, avg = run(2, rounds)
	t.Logf("并发 2 份  成功 %.1f%% 平均 %s", rate, avg.Round(time.Millisecond))
	rate, avg = run(3, rounds)
	t.Logf("并发 3 份  成功 %.1f%% 平均 %s", rate, avg.Round(time.Millisecond))
}

// TestHongguoLiveProbeCatalog 目录接口是否也存在概率性空响应。
func TestHongguoLiveProbeCatalog(t *testing.T) {
	requireProbe(t)
	d := probeDownloader(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if err := d.ensureHongguoDevice(ctx); err != nil {
		t.Fatalf("设备注册失败：%v", err)
	}
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
	tally := newTally("分类 landpage 单发")
	for i := 0; i < 30; i++ {
		started := time.Now()
		tally.add(probeHeaderSet(ctx, d, "/reading/distribution/category/landpage/v/", payload, false, false), time.Since(started))
		time.Sleep(120 * time.Millisecond)
	}
	t.Log(tally.String())
}
