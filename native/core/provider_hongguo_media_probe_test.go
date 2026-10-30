package core

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// hongguoTestMp4 造一个"moov 前置"的 mp4 头部：ftyp + moov{mvhd}。
// 红果真实片源就是这个布局（ftyp 32B + moov 11441B），所以 64KB 探测足够。
func hongguoTestMp4(duration time.Duration, version byte) []byte {
	mvhd := make([]byte, 100)
	mvhd[0] = version
	if version == 1 {
		binary.BigEndian.PutUint32(mvhd[20:], 1000)
		binary.BigEndian.PutUint64(mvhd[24:], uint64(duration/time.Millisecond))
	} else {
		binary.BigEndian.PutUint32(mvhd[12:], 1000)
		binary.BigEndian.PutUint32(mvhd[16:], uint32(duration/time.Millisecond))
	}
	moov := hongguoTestBox("moov", hongguoTestBox("mvhd", mvhd))
	ftyp := hongguoTestBox("ftyp", []byte("isomisom"))
	return append(ftyp, moov...)
}

func hongguoTestBox(boxType string, payload []byte) []byte {
	box := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint32(box, uint32(8+len(payload)))
	copy(box[4:], boxType)
	return append(box, payload...)
}

func TestHongguoMediaDurationFromMp4(t *testing.T) {
	for _, version := range []byte{0, 1} {
		got, err := hongguoMediaDurationFromPrefix(hongguoTestMp4(10*time.Second, version))
		if err != nil || got != 10*time.Second {
			t.Fatalf("v%d 10s: got %v err %v", version, got, err)
		}
		got, err = hongguoMediaDurationFromPrefix(hongguoTestMp4(79*time.Second+534*time.Millisecond, version))
		if err != nil || got != 79*time.Second+534*time.Millisecond {
			t.Fatalf("v%d 79.534s: got %v err %v", version, got, err)
		}
	}
}

func TestHongguoMediaDurationFromPlaylist(t *testing.T) {
	playlist := "#EXTM3U\n#EXTINF:4.0,\nseg0.ts\n#EXTINF:3.5,\nseg1.ts\n#EXT-X-ENDLIST\n"
	got, err := hongguoMediaDurationFromPrefix([]byte(playlist))
	if err != nil || got != 7*time.Second+500*time.Millisecond {
		t.Fatalf("playlist duration: got %v err %v", got, err)
	}
}

func TestHongguoMediaDurationUnknownForGarbage(t *testing.T) {
	if _, err := hongguoMediaDurationFromPrefix([]byte("<html>not media</html>")); err == nil {
		t.Fatal("garbage prefix accepted")
	}
	if _, err := hongguoMediaDurationFromPrefix(nil); err == nil {
		t.Fatal("empty prefix accepted")
	}
}

func TestHongguoLooksLikePreview(t *testing.T) {
	if !hongguoLooksLikePreview(10 * time.Second) {
		t.Fatal("10s clip should be treated as a preview")
	}
	if hongguoLooksLikePreview(79 * time.Second) {
		t.Fatal("79s episode must not be treated as a preview")
	}
	if hongguoLooksLikePreview(0) {
		t.Fatal("unknown duration must not be rejected")
	}
}

func TestHongguoPublicMediaRejectsPreviewClip(t *testing.T) {
	withProviderMirrorCacheForTest(t, map[string][]string{})
	clip := hongguoTestMp4(10*time.Second, 0)
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		response := sourceFixtureResponse(request, http.StatusPartialContent, string(clip))
		response.Header.Set("Content-Type", "video/mp4")
		return response, nil
	})
	media, err := d.resolveHongguoPublicMedia(context.Background(), "7694655440692448281")
	if err == nil {
		t.Fatalf("10 秒预告被当成正片返回了：%+v", media)
	}
	if !strings.Contains(err.Error(), "预告") {
		t.Fatalf("错误信息应说明是预告：%v", err)
	}
}

func TestHongguoPublicMediaAcceptsFullEpisode(t *testing.T) {
	withProviderMirrorCacheForTest(t, map[string][]string{})
	episode := hongguoTestMp4(79*time.Second, 0)
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		response := sourceFixtureResponse(request, http.StatusPartialContent, string(episode))
		response.Header.Set("Content-Type", "video/mp4")
		return response, nil
	})
	media, err := d.resolveHongguoPublicMedia(context.Background(), "7694655440692448281")
	if err != nil {
		t.Fatalf("正片被拒了：%v", err)
	}
	if media.Quality != 360 || media.Duration != 79*time.Second {
		t.Fatalf("unexpected media: %+v", media)
	}
}

// 命中 video_model 时应该直接用里面的 video_list，不再请求 fallback_api。
func TestHongguoAppMediaUsesVideoListFromModel(t *testing.T) {
	const videoID = "7694655440692448281"
	model := map[string]any{
		"status":         10,
		"message":        "success",
		"video_duration": 79.534,
		"video_list": []any{
			map[string]any{
				"main_url":   "https://v5-reading-video-d.qznovelvod.com/a/360p.mp4",
				"video_meta": map[string]any{"codec_type": "bytevc1", "vheight": 640, "definition": "360p"},
			},
			map[string]any{
				"main_url":   "https://v5-reading-video-d.qznovelvod.com/a/1080p.mp4",
				"video_meta": map[string]any{"codec_type": "bytevc1", "vheight": 1920, "definition": "1080p"},
			},
		},
		"fallback_api": map[string]any{"fallback_api": "https://vas-lf-x.snssdk.com/video/fplay/1/should-not-be-called"},
	}
	body, err := json.Marshal(map[string]any{
		"code": 0,
		"data": map[string]any{videoID: map[string]any{"video_model": model}},
	})
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "multi_video_model") {
			requests++
			return sourceFixtureResponse(request, http.StatusOK, string(body)), nil
		}
		// 0.8.16 第四批：取到地址后还要读一次头部，确认不是试看片段。
		return hongguoProbeResponse(request, 79*time.Second+534*time.Millisecond), nil
	})
	media, err := d.resolveHongguoAppMedia(context.Background(), videoID)
	if err != nil {
		t.Fatalf("取流失败：%v", err)
	}
	if !strings.HasSuffix(media.URL, "1080p.mp4") {
		t.Fatalf("应选最高清：%s", media.URL)
	}
	if media.Duration != 79*time.Second+534*time.Millisecond {
		t.Fatalf("时长应取 video_duration：%v", media.Duration)
	}
	if requests != 1 {
		t.Fatalf("只应请求一次 multi_video_model，实际 %d 次", requests)
	}
}

// 只给 10 秒的模型必须被拒，不能当正片。
func TestHongguoAppMediaRejectsPreviewModel(t *testing.T) {
	const videoID = "7694655440692448281"
	body, err := json.Marshal(map[string]any{
		"code": 0,
		"data": map[string]any{videoID: map[string]any{"video_model": map[string]any{
			"status":         10,
			"video_duration": 10.0,
			"video_list": []any{map[string]any{
				"main_url":   "https://v5-reading-video-d.qznovelvod.com/a/preview.mp4",
				"video_meta": map[string]any{"codec_type": "h264", "vheight": 640},
			}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusOK, string(body)), nil
	})
	if _, err := d.resolveHongguoAppMedia(context.Background(), videoID); err == nil || !strings.Contains(err.Error(), "预告") {
		t.Fatalf("10 秒模型应被拒，实际 err=%v", err)
	}
}

// video_model 自带 video_list 时，key_seed 可能被放在嵌套层（0.8.14）。
func TestHongguoVideoSeedFindsNestedSeed(t *testing.T) {
	cases := []struct {
		name string
		info map[string]any
		want string
	}{
		{"顶层", map[string]any{"key_seed": "top"}, "top"},
		{"video_info.data", map[string]any{
			"video_info": map[string]any{"data": map[string]any{"key_seed": "nested"}},
		}, "nested"},
		{"data", map[string]any{
			"data": map[string]any{"key_seed": "flat"},
		}, "flat"},
		{"都没有", map[string]any{"video_list": []any{}}, ""},
	}
	for _, item := range cases {
		if got := hongguoVideoSeed(item.info); got != item.want {
			t.Fatalf("%s：want %q, got %q", item.name, item.want, got)
		}
	}
}

// hongguoProbeResponse 造一个"时长探测"响应：返回指定时长的 mp4 头部。
//
// 0.8.16 第四批起，App 取流成功后还会读一次文件头（见
// verifyHongguoAppMedia），所以取流用例的 fixture 必须能响应这个请求。
func hongguoProbeResponse(request *http.Request, duration time.Duration) *http.Response {
	response := sourceFixtureResponse(request, http.StatusPartialContent, string(hongguoTestMp4(duration, 0)))
	response.Header.Set("Content-Type", "video/mp4")
	return response
}

// hongguoModelBody 把一份 video_model 包成取流接口的响应体。
func hongguoModelBody(t *testing.T, videoID string, model map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"code": 0,
		"data": map[string]any{videoID: map[string]any{"video_model": model}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// 元数据说 79 秒、文件头只有 10 秒 —— 必须拒。
//
// 这正是用户反馈「有的手机每集只能看 10 秒」的形态：服务端给某台设备的
// 地址是试看片段，但 video_duration 报的是整集时长，光看元数据完全正常。
func TestHongguoAppMediaRejectsPreviewByProbe(t *testing.T) {
	const videoID = "7694655440692448281"
	body := hongguoModelBody(t, videoID, map[string]any{
		"status":         10,
		"video_duration": 79.534,
		"video_list": []any{map[string]any{
			"main_url":   "https://v5-reading-video-d.qznovelvod.com/a/1080p.mp4",
			"video_meta": map[string]any{"codec_type": "h264", "vheight": 1920},
		}},
	})
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "multi_video_model") {
			return sourceFixtureResponse(request, http.StatusOK, body), nil
		}
		// 地址是"正片"名字，内容只有 10 秒。
		return hongguoProbeResponse(request, 10*time.Second), nil
	})
	_, err := d.resolveHongguoAppMedia(context.Background(), videoID)
	if err == nil {
		t.Fatal("10 秒试看片段被当成正片返回了")
	}
	if !strings.Contains(err.Error(), "试看") {
		t.Fatalf("错误信息应说明是试看片段：%v", err)
	}
}

// 最高清那档被换成试看时，要退到下一个正常档位，而不是整集报错。
func TestHongguoAppMediaSkipsPreviewDefinition(t *testing.T) {
	const videoID = "7694655440692448281"
	body := hongguoModelBody(t, videoID, map[string]any{
		"status":         10,
		"video_duration": 79.534,
		"video_list": []any{
			map[string]any{
				"main_url":   "https://v5-reading-video-d.qznovelvod.com/a/1080p.mp4",
				"video_meta": map[string]any{"codec_type": "h264", "vheight": 1920},
			},
			map[string]any{
				"main_url":   "https://v5-reading-video-d.qznovelvod.com/a/360p.mp4",
				"video_meta": map[string]any{"codec_type": "h264", "vheight": 640},
			},
		},
	})
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "multi_video_model") {
			return sourceFixtureResponse(request, http.StatusOK, body), nil
		}
		if strings.Contains(request.URL.Path, "1080p") {
			return hongguoProbeResponse(request, 10*time.Second), nil
		}
		return hongguoProbeResponse(request, 79*time.Second+534*time.Millisecond), nil
	})
	media, err := d.resolveHongguoAppMedia(context.Background(), videoID)
	if err != nil {
		t.Fatalf("应退到 360p 正常档位，实际报错：%v", err)
	}
	if !strings.HasSuffix(media.URL, "360p.mp4") {
		t.Fatalf("应选 360p，实际 %s", media.URL)
	}
	if media.Duration != 79*time.Second+534*time.Millisecond {
		t.Fatalf("时长应取实测值：%v", media.Duration)
	}
}

// 档位自己标称只有 10 秒时，直接剔掉，不必等探测。
func TestHongguoAppMediaSkipsShortDefinitionByMetadata(t *testing.T) {
	const videoID = "7694655440692448281"
	body := hongguoModelBody(t, videoID, map[string]any{
		"status":         10,
		"video_duration": 79.534,
		"video_list": []any{
			map[string]any{
				"main_url":   "https://v5-reading-video-d.qznovelvod.com/a/1080p.mp4",
				"duration":   10.0,
				"video_meta": map[string]any{"codec_type": "h264", "vheight": 1920},
			},
			map[string]any{
				"main_url":   "https://v5-reading-video-d.qznovelvod.com/a/360p.mp4",
				"duration":   79.5,
				"video_meta": map[string]any{"codec_type": "h264", "vheight": 640},
			},
		},
	})
	probes := 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "multi_video_model") {
			return sourceFixtureResponse(request, http.StatusOK, body), nil
		}
		probes++
		if strings.Contains(request.URL.Path, "1080p") {
			t.Fatal("标称 10 秒的档位不该再被探测")
		}
		return hongguoProbeResponse(request, 79*time.Second), nil
	})
	media, err := d.resolveHongguoAppMedia(context.Background(), videoID)
	if err != nil {
		t.Fatalf("应选 360p，实际报错：%v", err)
	}
	if !strings.HasSuffix(media.URL, "360p.mp4") {
		t.Fatalf("应选 360p，实际 %s", media.URL)
	}
	if probes != 1 {
		t.Fatalf("只应探测选中的那一档，实际 %d 次", probes)
	}
}

// 设备号"注册过"但服务端已经不认时，要换设备重注册再取，而不是直接掉到
// 只给 10 秒预告的公开片源上。
func TestHongguoAppMediaReregistersWhenDeviceForgotten(t *testing.T) {
	const videoID = "7694655440692448281"
	body := hongguoModelBody(t, videoID, map[string]any{
		"status":         10,
		"video_duration": 79.534,
		"video_list": []any{map[string]any{
			"main_url":   "https://v5-reading-video-d.qznovelvod.com/a/1080p.mp4",
			"video_meta": map[string]any{"codec_type": "h264", "vheight": 1920},
		}},
	})
	registrations := 0
	models := 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "device_register") {
			registrations++
			return sourceFixtureResponse(request, http.StatusOK,
				`{"device_id_str":"76338817222425","install_id_str":"76338817226521"}`), nil
		}
		if strings.Contains(request.URL.Path, "multi_video_model") {
			models++
			if registrations == 0 {
				// 旧设备号被服务端遗忘 → 200 + 空正文
				return sourceFixtureResponse(request, http.StatusOK, ""), nil
			}
			return sourceFixtureResponse(request, http.StatusOK, body), nil
		}
		return hongguoProbeResponse(request, 79*time.Second+534*time.Millisecond), nil
	})
	client := d.hongguoClient()
	client.mu.Lock()
	client.state.Registered = true
	client.state.DeviceID = "1111111111111111"
	client.state.InstallID = "2222222222222222"
	client.mu.Unlock()

	media, err := d.resolveHongguoAppMedia(context.Background(), videoID)
	if err != nil {
		t.Fatalf("换设备重试后应能取到正片，实际报错：%v", err)
	}
	if !strings.HasSuffix(media.URL, "1080p.mp4") {
		t.Fatalf("取到的地址不对：%s", media.URL)
	}
	if registrations != 1 {
		t.Fatalf("应重新注册一次，实际 %d 次", registrations)
	}
	client.mu.Lock()
	deviceID := client.state.DeviceID
	client.mu.Unlock()
	if deviceID != "76338817222425" {
		t.Fatalf("设备号应换成新注册的，实际 %s", deviceID)
	}
	if models < 2 {
		t.Fatalf("空正文后应再取一次，实际取流 %d 次", models)
	}
}

// 公开片源读不出时长时必须拒收（以前按"未知"放行，网络抖动就会漏出 10 秒预告）。
func TestHongguoPublicMediaRejectsUnknownDuration(t *testing.T) {
	withProviderMirrorCacheForTest(t, map[string][]string{})
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		response := sourceFixtureResponse(request, http.StatusPartialContent, "<html>not media</html>")
		response.Header.Set("Content-Type", "video/mp4")
		return response, nil
	})
	if _, err := d.resolveHongguoPublicMedia(context.Background(), "7694655440692448281"); err == nil {
		t.Fatal("读不出时长时应拒收，不能放行")
	}
}
