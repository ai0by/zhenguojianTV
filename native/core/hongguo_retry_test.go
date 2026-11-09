package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 0.8.22：取流接口偶尔会返回"有 JSON 但没有 video_model"（服务端降级 /
// 那一发签名没中）。用户点一次重试就好了 —— 说明它本来就是瞬时的。
// 0.8.27：这里不再「原地重发同一条路径」，而是按
// GET video_tab → 旧 POST 主域 → 旧 POST 备域**单发即换**。断言的仍是
// 「一次空 model 之后还能出片，且不作废目录侧设备号」。
func TestHongguoAppMediaFallsBackToNextRouteOnEmptyModel(t *testing.T) {
	const videoID = "7694655440692448281"
	good := hongguoModelBody(t, videoID, map[string]any{
		"status":         10,
		"video_duration": 79.534,
		"video_list": []any{map[string]any{
			"main_url":   "https://v5-reading-video-d.qznovelvod.com/a/720p.mp4",
			"video_meta": map[string]any{"codec_type": "bytevc1", "vheight": 1280},
		}},
	})
	var modelCalls, probeCalls int32
	d := sourceFixtureDownloader(t, withHongguoPlaybackRegistration(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "multi_video_model") {
			// 第一条路由（GET video_tab）：降级，给了 JSON 但 data 里没有 video_model。
			if atomic.AddInt32(&modelCalls, 1) == 1 {
				return sourceFixtureResponse(request, http.StatusOK, `{"code":0,"data":{}}`), nil
			}
			return sourceFixtureResponse(request, http.StatusOK, good), nil
		}
		atomic.AddInt32(&probeCalls, 1)
		return hongguoProbeResponse(request, 79*time.Second+534*time.Millisecond), nil
	}))
	media, err := d.resolveHongguoAppMedia(context.Background(), videoID)
	if err != nil {
		t.Fatalf("空 model 后应原地重试成功：%v", err)
	}
	if !strings.HasSuffix(media.URL, "720p.mp4") {
		t.Fatalf("地址不对：%s", media.URL)
	}
	if got := atomic.LoadInt32(&modelCalls); got != 2 {
		t.Fatalf("应请求两次 multi_video_model，实际 %d", got)
	}
	// 目录侧设备号不能被换掉：0.8.27 的播放身份是**单轮专用**的，刻不写回
	// hongguoCatalogState，否则每播一集都会打断目录 / 详情 / 弹幕的会话。
	client := d.hongguoClient()
	client.mu.Lock()
	registered := client.state.Registered
	deviceID := client.state.DeviceID
	client.mu.Unlock()
	if !registered || deviceID == "" {
		t.Fatalf("原地重试不该作废设备：registered=%v id=%q", registered, deviceID)
	}
}

// 一直空 model 时才认作真失败（此时交给上层提示用户）。
func TestHongguoAppMediaGivesUpAfterRepeatedEmptyModel(t *testing.T) {
	const videoID = "7694655440692448281"
	var calls int32
	d := sourceFixtureDownloader(t, withHongguoPlaybackRegistration(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "multi_video_model") {
			atomic.AddInt32(&calls, 1)
			return sourceFixtureResponse(request, http.StatusOK, `{"code":0,"data":{}}`), nil
		}
		return hongguoProbeResponse(request, 79*time.Second), nil
	}))
	if _, err := d.resolveHongguoAppMedia(context.Background(), videoID); err == nil {
		t.Fatal("连续空 model 应当报错")
	}
	// 三条路由单发 + 换一个全新身份只重试首选路径一次，最多 4 发；
	// 旧链路的 5 次空正文 + 跨窗退避已经删掉，再多就是在空转。
	if got := atomic.LoadInt32(&calls); got > 4 {
		t.Fatalf("空 model 的重试次数过多：%d", got)
	}
}

// 预取失败不能进失败缓存：用户紧接着切到这一集，会被这份瞬时失败挡住。
func TestPrefetchDanmakuFailureNotCached(t *testing.T) {
	var calls int32
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		// data_list 在、但不是数组：这才是真的格式异常。
		return sourceFixtureResponse(request, http.StatusOK, `{"code":0,"data":{"data_list":"oops"}}`), nil
	})
	prefetchCtx := context.WithValue(context.Background(), danmakuPrefetchKey{}, true)
	if _, err := d.hongguoDanmaku(prefetchCtx, "6001", "7001", 0, 30_000); err == nil {
		t.Fatal("data_list 不是数组时应当失败")
	}
	client := d.hongguoClient()
	client.mu.Lock()
	_, cached := client.danmaku["6001:7001:0"]
	client.mu.Unlock()
	if cached {
		t.Fatal("预取失败不该写进失败缓存")
	}
	// 正式请求失败才写缓存 —— 用来挡住同一窗口被连点打爆。
	if _, err := d.hongguoDanmaku(context.Background(), "6001", "7001", 0, 30_000); err == nil {
		t.Fatal("data_list 不是数组时应当失败")
	}
	client.mu.Lock()
	_, cached = client.danmaku["6001:7001:0"]
	client.mu.Unlock()
	if !cached {
		t.Fatal("正式请求的失败应当写进缓存")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("应各请求一次，实际 %d", got)
	}
}

// 整集没有弹幕数据时，服务端可能连 data / data_list 都不下发。
// 这种"根本没有这个字段"要按空窗口处理，不能判失败（0.8.22）。
func TestParseHongguoDanmakuMissingDataBlock(t *testing.T) {
	for _, body := range []string{
		`{"code":0}`,
		`{"code":0,"data":{}}`,
		`{"code":0,"data":{"data_list":[]}}`,
	} {
		var result map[string]any
		decoder := json.NewDecoder(strings.NewReader(body))
		decoder.UseNumber()
		if err := decoder.Decode(&result); err != nil {
			t.Fatal(err)
		}
		page, err := parseHongguoDanmaku(result, "7001", 60_000, 120_000)
		if err != nil {
			t.Fatalf("%s 应视为空窗口：%v", body, err)
		}
		if page.StartMS != 60_000 || page.NextMS != 90_000 || len(page.Items) != 0 {
			t.Fatalf("%s 的空窗口不对：%+v", body, page)
		}
	}
	// 字段在但不是数组 —— 这才是真格式异常。
	var broken map[string]any
	decoder := json.NewDecoder(strings.NewReader(`{"code":0,"data":{"data_list":"oops"}}`))
	decoder.UseNumber()
	if err := decoder.Decode(&broken); err != nil {
		t.Fatal(err)
	}
	if _, err := parseHongguoDanmaku(broken, "7001", 0, 60_000); err == nil {
		t.Fatal("data_list 不是数组时应当报错")
	}
}

// 取流拿不到片源时要带上 media_transient 标记，前端才知道"值得自己再试"。
func TestHongguoMediaEmptyModelIsTransient(t *testing.T) {
	const videoID = "7694655440692448281"
	// 注册要成功：这条用例验证的是「注册没问题、但上游就是不给 model」时
	// 上层仍标记瞬时失败；注册失败是另一条用例的短路分支。
	d := sourceFixtureDownloader(t, withHongguoPlaybackRegistration(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "multi_video_model") {
			return sourceFixtureResponse(request, http.StatusOK, `{"code":0,"data":{}}`), nil
		}
		return sourceFixtureResponse(request, http.StatusNotFound, ""), nil
	}))
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		return d.client.Transport.RoundTrip(request)
	})
	_, err := engine.downloader.resolveHongguoMedia(context.Background(), Task{
		DramaID: "hongguo:6001",
		Chapter: Chapter{Source: "hongguo", VideoURL: "hongguo-cenc://" + videoID},
	})
	if err == nil {
		t.Fatal("空 model 且无兜底片源时应失败")
	}
	if !errors.Is(err, errNativeMediaTransient) {
		t.Fatalf("应标记成瞬时失败，实际：%v", err)
	}
}
