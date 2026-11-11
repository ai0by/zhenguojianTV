package core

// 0.8.27 播放专用取流链路的回归测试（2026-11-11 随签名修复更新）。
//
// 「一个注册身份只能成功取流一次」已在 2026-11-11 被实测推翻（同一身份连取
// 4 集 4/4 成功），真正原因是取流接口改认播放器专用签名。每轮换身份的机制
// 保留作容错，这里继续钉住「GET tab 优先」「单发即换」「失败不缓存」「全失败
// 再注册一次」，避免改回共用 hongguoAppRequest 的秒级退避重试。
// 签名头本身的断言在 provider_hongguo_player_sign_test.go。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHongguoPlaybackRoutesPreferVideoTab(t *testing.T) {
	const videoID = "7605502162419846206"
	routes := hongguoPlaybackRoutes(videoID)
	if len(routes) != 3 {
		t.Fatalf("应有三条路由，实际 %d", len(routes))
	}
	want := []struct {
		name   string
		method string
		path   string
	}{
		{"video_tab", http.MethodGet, hongguoPlaybackTabPath},
		{"legacy_primary", http.MethodPost, hongguoPlaybackLegacyPath},
		{"legacy_backup", http.MethodPost, hongguoPlaybackLegacyPath},
	}
	for index, expected := range want {
		if routes[index].name != expected.name || routes[index].method != expected.method || routes[index].path != expected.path {
			t.Fatalf("第 %d 条路由不对：%+v", index, routes[index])
		}
	}
	if routes[0].body != nil {
		t.Fatal("GET video_tab 不应带 POST body")
	}
	if routes[1].body == nil || routes[2].body == nil {
		t.Fatal("旧 POST 路由必须带 mixed_video_id_map")
	}
	// 少任何一个 snake_case 参数都会被上游判 100103 PARAM_INVALID。
	required := map[string]string{
		"video_id":                  videoID,
		"video_id_type":             "0",
		"device_level":              "1",
		"need_all_video_definition": "1",
		"need_mp4_align":            "0",
		"use_os_player":             "0",
		"video_platform":            "1024",
		"source":                    "0",
		"from_video_id":             "",
	}
	for key, value := range required {
		if !routes[0].extra.Has(key) || routes[0].extra.Get(key) != value {
			t.Fatalf("GET tab 参数 %s 缺失或不对：%q", key, routes[0].extra.Get(key))
		}
	}
}

// GET tab 的 video_model 可能是 JSON 字符串，容器也换成了 video_model_data。
func TestHongguoPlaybackModelFromVideoTab(t *testing.T) {
	const videoID = "7605502162419846206"
	result := map[string]any{
		"code": "0",
		"data": map[string]any{
			"video_model_data": map[string]any{
				videoID: map[string]any{
					"video_model": `{"video_duration":79.5,"video_list":{"video_1":{"main_url":"https://v5-reading-video-d.qznovelvod.com/a/720p.mp4"}}}`,
				},
			},
		},
	}
	model, err := hongguoPlaybackModelFromResult(result, videoID)
	if err != nil {
		t.Fatalf("应解析出 GET tab 的 model：%v", err)
	}
	if got := mapString(model, "video_duration"); got != "79.5" {
		t.Fatalf("model 内容不对：%q", got)
	}
	if _, err := hongguoPlaybackModelFromResult(map[string]any{"code": 0, "data": map[string]any{}}, videoID); !errors.Is(err, errHongguoAppEmptyModel) {
		t.Fatalf("空 model 应返回哨兵，实际 %v", err)
	}
}

// 三条路径全失败时，第二轮要换一个**全新**身份，并且只重试首选路径一次。
func TestHongguoPlaybackRetriesWithAnotherFreshIdentity(t *testing.T) {
	const videoID = "7605502162419846206"
	registrations := 0
	var modelPaths []string
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		path := request.URL.Path
		if strings.Contains(path, "device_register") {
			registrations++
			device := fmt.Sprintf("7633881722242%d", registrations)
			return sourceFixtureResponse(request, http.StatusOK,
				fmt.Sprintf(`{"device_id_str":"%s","install_id_str":"7633881722652%d"}`, device, registrations)), nil
		}
		if strings.Contains(path, "multi_video_model") {
			modelPaths = append(modelPaths, path)
			// 上游「按身份静默屏蔽」的典型形态：200 + 0 字节。
			return sourceFixtureResponse(request, http.StatusOK, ""), nil
		}
		return hongguoProbeResponse(request, 79*time.Second), nil
	})
	_, err := d.resolveHongguoAppMedia(context.Background(), videoID)
	if !errors.Is(err, errHongguoAppEmptyModel) {
		t.Fatalf("全失败应收敛成空 model 哨兵，实际 %v", err)
	}
	if registrations != 2 {
		t.Fatalf("第二轮应再注册一个新身份，实际注册 %d 次", registrations)
	}
	if len(modelPaths) != 4 {
		t.Fatalf("应为「三条单发 + 首选路径一次」，实际取流 %d 次：%v", len(modelPaths), modelPaths)
	}
	// 失败结果绝不进短缓存：紧接着重试要能立刻重新发请求。
	client := d.hongguoClient()
	client.mu.Lock()
	cached := len(client.mediaCache)
	client.mu.Unlock()
	if cached != 0 {
		t.Fatalf("失败结果不该写缓存，实际 %d 条", cached)
	}
}

// 0.8.27 构建前 12 集复测里唯一一次失败：注册抖动超时后，第二轮跳过了
// 重新注册，拿目录侧旧身份单发必然空正文，最后硬报「设备注册失败」。
// 注册是暂态抖动，第二轮必须再注册一次新身份并成功出片。
func TestHongguoPlaybackReregistersAfterTransientRegisterFailure(t *testing.T) {
	const videoID = "7694655440692448281"
	const freshDevice = "76338817222425"
	const catalogDevice = "1111111111111111"
	body := hongguoModelBody(t, videoID, map[string]any{
		"status":         10,
		"video_duration": 79.534,
		"video_list": []any{map[string]any{
			"main_url":   "https://v5-reading-video-d.qznovelvod.com/a/1080p.mp4",
			"video_meta": map[string]any{"codec_type": "h264", "vheight": 1920},
		}},
	})
	registrations := 0
	var modelDeviceIDs []string
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "device_register") {
			registrations++
			// 第一轮整组注册尝试都失败；之后恢复。
			if registrations <= hongguoDeviceRegisterAttempts {
				return nil, errors.New("注册接口偶发超时")
			}
			return sourceFixtureResponse(request, http.StatusOK,
				`{"device_id_str":"76338817222425","install_id_str":"76338817226521"}`), nil
		}
		if strings.Contains(request.URL.Path, "multi_video_model") {
			device := request.URL.Query().Get("device_id")
			modelDeviceIDs = append(modelDeviceIDs, device)
			// 只有新注册的身份能出片，目录侧旧身份按机制必然静默回空。
			if device == freshDevice {
				return sourceFixtureResponse(request, http.StatusOK, body), nil
			}
			return sourceFixtureResponse(request, http.StatusOK, ""), nil
		}
		return hongguoProbeResponse(request, 79*time.Second+534*time.Millisecond), nil
	})
	client := d.hongguoClient()
	client.mu.Lock()
	client.state.Registered = true
	client.state.DeviceID = catalogDevice
	client.state.InstallID = "2222222222222222"
	client.mu.Unlock()

	media, err := d.resolveHongguoAppMedia(context.Background(), videoID)
	if err != nil {
		t.Fatalf("注册抖动应在第二轮自愈，实际报错：%v", err)
	}
	if !strings.HasSuffix(media.URL, "1080p.mp4") {
		t.Fatalf("取到的地址不对：%s", media.URL)
	}
	// 第一轮 3 次失败 + 第二轮 1 次成功。
	if want := hongguoDeviceRegisterAttempts + 1; registrations != want {
		t.Fatalf("第二轮应重新注册，实际注册请求 %d 次（期望 %d）", registrations, want)
	}
	if len(modelDeviceIDs) == 0 || modelDeviceIDs[len(modelDeviceIDs)-1] != freshDevice {
		t.Fatalf("第二轮必须用新注册的身份取流，实际 %v", modelDeviceIDs)
	}
}
