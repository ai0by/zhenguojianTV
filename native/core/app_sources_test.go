package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type sourceFixtureTransport func(*http.Request) (*http.Response, error)

func (transport sourceFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func sourceFixtureResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: request}
}

func sourceFixtureDownloader(t *testing.T, transport sourceFixtureTransport) *Downloader {
	t.Helper()
	downloader := &Downloader{cfg: Config{dataDir: t.TempDir(), Retries: 1, PageSize: 30, MaxPagesPerSort: 1},
		client: &http.Client{Transport: transport}, limiter: newRequestLimiter(3, 0), providerHosts: map[string]string{}}
	// 0.8.14：红果取流前会确保游客设备已注册，注册请求会走同一个 fixture
	// transport，把「只请求一次取流接口」这类断言全部打破。单测里直接标记
	// 成已注册，注册逻辑本身由 provider_hongguo_device_test.go 单独覆盖。
	markHongguoDeviceRegistered(downloader)
	return downloader
}

// markHongguoDeviceRegistered 把红果客户端标成"设备已注册"，跳过注册请求。
func markHongguoDeviceRegistered(downloader *Downloader) {
	client := downloader.hongguoClient()
	client.mu.Lock()
	client.state.Registered = true
	client.mu.Unlock()
}

func sourceFixtureEngine(t *testing.T, transport sourceFixtureTransport) *nativeEngine {
	t.Helper()
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.downloads.close)
	engine.downloader.client.Transport = transport
	engine.downloader.limiter = newRequestLimiter(3, 0)
	engine.downloader.cfg.Retries = 1
	markHongguoDeviceRegistered(engine.downloader)
	return engine
}

func awaitSourceTask(t *testing.T, engine *nativeEngine, source string) nativeSourceStatus {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status := engine.sourceStatus(source)
		if !status.Running {
			return status
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("source task did not finish")
	return nativeSourceStatus{}
}

func TestSourceUpdatesPreservePagesOtherSourcesAndMetadata(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		if !strings.Contains(request.URL.Path, "/api/videos/category/") {
			return nil, errors.New("only catalog text is allowed")
		}
		id := "new"
		if request.URL.Query().Get("page") == "3" {
			id = "continued"
		}
		return sourceFixtureResponse(request, 200, fmt.Sprintf(`{"data":[{"id":%q,"title":"合成短剧","desc":"完整资料","totalEpisode":2}]}`, id)), nil
	})
	engine.catalogs[sourceHuangguoAI] = []nativeDrama{{ID: "huangguoai:old", Source: sourceHuangguoAI, Title: "原有短剧", Description: "保留资料", Episodes: 2}}
	engine.catalogStates[sourceHuangguoAI] = nativeCatalogState{Page: 2, HasMore: true}
	engine.catalogs[sourceHongguo] = []nativeDrama{{ID: "hongguo:1", Source: sourceHongguo, Title: "其他站源"}}
	if err := engine.updateSource(context.Background(), sourceHuangguoAI, "update"); err != nil {
		t.Fatal(err)
	}
	page := engine.nativeCached(sourceHuangguoAI)
	if len(page.Items) != 3 || page.Page != 3 || page.HasMore {
		t.Fatalf("lost refresh/tail state: %+v", page)
	}
	if len(engine.nativeCached(sourceHongguo).Items) != 1 {
		t.Fatal("updated another source")
	}
	engine.saveCatalogCache(sourceHuangguoAI, &nativeCatalogResult{Items: []nativeDrama{{ID: "huangguoai:old", Title: "新名称"}}, Page: 1, HasMore: true})
	page = engine.nativeCached(sourceHuangguoAI)
	if page.Page != 3 || page.HasMore || len(page.Items) != 3 || page.Items[0].Description != "保留资料" {
		t.Fatal("head refresh discarded cached metadata or pagination", page)
	}
}

func TestSourceJobsDeduplicateCancelAndRecoverAfterRestart(t *testing.T) {
	if buildAllSources != "true" {
		t.Skip("full edition source lifecycle")
	}
	var calls atomic.Int32
	started := make(chan struct{})
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	engine.catalogs[sourceHuangguoVideo] = []nativeDrama{{ID: "huangguo-video:video/fixture", Source: sourceHuangguoVideo, Title: "已有缓存"}}
	if _, err := engine.startSourceTask(sourceHuangguoVideo, "update", nativeDrama{}); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := engine.startSourceTask(sourceHuangguoVideo, "check", nativeDrama{}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || engine.sourceStatus(sourceHuangguoVideo).Operation != "update" {
		t.Fatal("duplicate request replaced the running task")
	}
	if count, _ := engine.workLease("", ""); count != 1 {
		t.Fatal("background service cannot see source work")
	}
	engine.cancelSourceTask(sourceHuangguoVideo)
	status := awaitSourceTask(t, engine, sourceHuangguoVideo)
	if status.Count != 1 || status.Stage != "已停止" {
		t.Fatal("cancel discarded cache or did not stop", status)
	}
	engine.changeSourceRecord(sourceHuangguoVideo, func(record *nativeSourceRecord) { record.Running = true })
	restored := &nativeEngine{directory: engine.directory}
	restored.loadSourceRecords()
	if record := restored.sourceRecords[sourceHuangguoVideo]; record.Running || record.Error == "" {
		t.Fatal("interrupted work stayed running")
	}
}

func TestSourceHealthRejectsHTML200AndPreservesFailureStage(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/videos":
			return sourceFixtureResponse(request, 200, `<article class="video-card"><a href="/video/fixture" title="合成检测剧">合成检测剧</a></article>`), nil
		case "/video/fixture":
			return sourceFixtureResponse(request, 200, `<h1>合成检测剧</h1><div data-hls="https://media.example.test/master.m3u8"></div>`), nil
		case "/master.m3u8":
			response := sourceFixtureResponse(request, 200, `<html><title>Just a moment</title><script>_cf_chl_opt={}</script>Cloudflare</html>`)
			response.Header.Set("Content-Type", "text/html")
			return response, nil
		default:
			t.Error("unexpected resource, including images", request.URL.Path)
			return nil, errors.New("blocked")
		}
	})
	err := engine.checkSource(context.Background(), sourceHuangguoVideo, nativeDrama{}, true)
	status := engine.sourceStatus(sourceHuangguoVideo)
	if err == nil || status.Health == nil || status.Health.State != "failed" || len(status.Health.Steps) != 3 || status.Health.Steps[2].State != "failed" {
		t.Fatal("HTML 200 was reported as playable", err, status.Health)
	}
	if status.Health.Steps[2].HTTPStatus != 200 || !strings.Contains(status.Health.Steps[2].Message, "浏览器验证") {
		t.Fatal("missing CF failure detail", status.Health.Steps)
	}
}

func TestSourceHealthChecksHLSKeyAndBoundedMedia(t *testing.T) {
	var segments atomic.Int32
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/videos":
			return sourceFixtureResponse(request, 200, `<article class="video-card"><a href="/video/fixture" title="合成检测剧">合成检测剧</a></article>`), nil
		case "/video/fixture":
			return sourceFixtureResponse(request, 200, `<div data-hls="https://huangguo.video/master.m3u8"></div>`), nil
		case "/master.m3u8":
			return sourceFixtureResponse(request, 200, "#EXTM3U\n#EXT-X-STREAM-INF:RESOLUTION=320x480\nmedia.m3u8?signature=a%2Fb\n"), nil
		case "/media.m3u8":
			if request.URL.RawQuery != "signature=a%2Fb" {
				t.Error("signed media URL changed")
			}
			return sourceFixtureResponse(request, 200, "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"/api/hls_key/fixture\"\n#EXTINF:4,\nsegment.ts\n#EXT-X-ENDLIST"), nil
		case "/api/preview-token":
			return sourceFixtureResponse(request, 200, `{"ok":true,"token":"fixture-preview","expires_in":300}`), nil
		case "/api/hls_key/fixture":
			if request.Header.Get("X-Preview-Token") != "fixture-preview" {
				t.Error("missing preview token")
			}
			return sourceFixtureResponse(request, 200, "0123456789abcdef"), nil
		case "/segment.ts":
			segments.Add(1)
			if request.Header.Get("Range") != "bytes=0-1023" || request.Header.Get("X-Preview-Token") != "" {
				t.Error("unbounded probe or leaked token")
			}
			return sourceFixtureResponse(request, 206, strings.Repeat("s", 2048)), nil
		default:
			t.Error("unexpected resource", request.URL.Path)
			return nil, errors.New("blocked")
		}
	})
	if err := engine.checkSource(context.Background(), sourceHuangguoVideo, nativeDrama{}, true); err != nil {
		t.Fatal(err)
	}
	health := engine.sourceStatus(sourceHuangguoVideo).Health
	if health.State != "ok" || len(health.Steps) != 5 || segments.Load() != 1 {
		t.Fatal("incomplete health report", health)
	}
	body, err := os.ReadFile(filepath.Join(engine.directory, "sources.json"))
	if err != nil || strings.Contains(string(body), "fixture-preview") {
		t.Fatal("health persistence leaked preview token", err)
	}
	var saved map[string]nativeSourceRecord
	if json.Unmarshal(body, &saved) != nil || saved[sourceHuangguoVideo].Health == nil {
		t.Fatal("health report was not persisted")
	}
}

func TestSourceBackoffUsesRelativeTimeAndRetainsTypedError(t *testing.T) {
	backoff := &requestBackoff{host: "huangguo.video", status: 403, until: time.Now().Add(time.Minute)}
	if !strings.Contains(backoff.Error(), "秒后") || strings.Contains(backoff.Error(), backoff.until.Format("15:04:05")) {
		t.Fatal("retry time depends on native timezone")
	}
	var restored *requestBackoff
	if !errors.As(publicError(fmt.Errorf("playlist: %w", backoff)), &restored) {
		t.Fatal("redaction discarded structured retry time")
	}
}

// 0.8.10：退避期只该挡住后台资料补齐，用户主动点的任务要放行。
//
// 用户反馈"红果失败后一直卡着，去设置里点一次检查链接才好"：上一次失败挂上
// host 退避后，连「更新剧库 / 检查链接」都被挡回去，只能干等。
func TestSourceTaskBackoffOnlyHoldsBackgroundOperations(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	engine.changeSourceRecord(sourceHongguo, func(record *nativeSourceRecord) {
		record.RetryAt = time.Now().Add(10 * time.Minute)
	})
	_, err := engine.startSourceTask(sourceHongguo, "metadata", nativeDrama{})
	if err == nil || !strings.Contains(err.Error(), "暂停请求") {
		t.Fatalf("background metadata task ignored the host backoff: %v", err)
	}
	if _, err := engine.startSourceTask(sourceHongguo, "update", nativeDrama{}); err != nil {
		t.Fatalf("user-initiated update was blocked by the host backoff: %v", err)
	}
	engine.cancelSourceTask(sourceHongguo)
	awaitSourceTask(t, engine, sourceHongguo)
}
