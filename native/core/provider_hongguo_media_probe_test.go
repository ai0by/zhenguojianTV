package core

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
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
		requests++
		if !strings.Contains(request.URL.Path, "multi_video_model") {
			return nil, errors.New("不该请求 " + request.URL.Path)
		}
		return sourceFixtureResponse(request, http.StatusOK, string(body)), nil
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
