package core

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestHongguoBackupAddressesAndCodecAlternatives(t *testing.T) {
	media, err := selectHongguoAppVideoList(map[string]any{"video_list": []any{
		map[string]any{"main_url": "https://media.test.qznovelvod.com/hevc.mp4", "video_meta": map[string]any{"codec_type": "hevc", "definition": "1080p"}},
		map[string]any{
			"main_url":    base64.StdEncoding.EncodeToString([]byte("https://media.test.qznovelvod.com/main.mp4?token=a%2Fb")),
			"backup_url":  "https://backup.test.qznovelvod.com/main.mp4?token=unchanged",
			"backup_urls": []any{"https://backup.test.qznovelvod.com/main.mp4?token=unchanged", "file:///invalid", "https://third.test.qznovelvod.com/main.mp4"},
			"video_meta":  map[string]any{"codec_type": "h264", "definition": "1080p"},
		},
		map[string]any{"main_url": "https://media.test.qznovelvod.com/low.mp4", "video_meta": map[string]any{"codec_type": "h264", "definition": "720p"}},
		map[string]any{"main_url": "https://media.test.qznovelvod.com/unsupported.mp4", "backup_url": "https://backup.test.qznovelvod.com/unsupported.mp4", "video_meta": map[string]any{"codec_type": "bytevc2", "definition": "2160p"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	choices := nativePlaybackChoices(media, 0)
	var addresses []string
	for _, option := range choices.media {
		addresses = append(addresses, option.URL)
	}
	want := []string{
		"https://media.test.qznovelvod.com/main.mp4?token=a%2Fb", "https://backup.test.qznovelvod.com/main.mp4?token=unchanged",
		"https://third.test.qznovelvod.com/main.mp4", "https://media.test.qznovelvod.com/hevc.mp4", "https://media.test.qznovelvod.com/low.mp4",
	}
	if !reflect.DeepEqual(addresses, want) || !reflect.DeepEqual(choices.qualities, []int{1080, 720}) {
		t.Fatalf("wrong fallback order: %+v; qualities=%v", addresses, choices.qualities)
	}
	if selected := nativePlaybackChoices(media, 1080); len(selected.media) != 4 {
		t.Fatal("manual quality must preserve its backup codecs and exclude lower quality")
	}
	if selected := nativePlaybackChoices(media, 720); len(selected.media) != 1 || selected.media[0].Quality != 720 {
		t.Fatal("manual 720p selection changed")
	}
	if selected := nativePlaybackChoices(media, 2160); len(selected.media) != len(want) {
		t.Fatal("an unavailable quality should use the episode's available routes")
	}
}

func TestNativePlaybackFallbackPreservesKeysAndReleasesOnlyOldSession(t *testing.T) {
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := providerMedia{URL: "https://first.test/video.mp4", Referer: "https://first.test/", CENCKey: []byte("0123456789abcdef"), Quality: 1080}
	second := providerMedia{URL: "https://second.test/video.mp4", Referer: "https://second.test/watch", CENCKey: []byte("fedcba9876543210"), Quality: 1080}
	first.Variants = []providerMedia{first, first, second}
	plan, err := engine.nativeOpenPlayback(context.Background(), nativePlaybackChoices(first, 0))
	if err != nil || plan.RouteCount != 2 || plan.Session == "" {
		t.Fatalf("initial playback: %+v, %v", plan, err)
	}
	next, err := engine.nativeNextPlayback(context.Background(), plan.Session)
	if err != nil {
		t.Fatal(err)
	}
	// nativeOpenPlayback 会有意把上游地址换成本地代理地址（播放全代理），
	// 所以这里不能原样比对 second.URL，只能校验代理前缀 + 凭据是否跟着换了。
	if !strings.HasPrefix(next.URL, "http://127.0.0.1:") || next.URL == second.URL ||
		next.Headers["Referer"] != second.Referer || next.Key != "66656463626139383736353433323130" ||
		next.RouteIndex != 1 || next.Session == plan.Session {
		t.Fatalf("alternate route lost its credentials: %+v", next)
	}
	engine.nativeReleasePlayback(plan.Session)
	if _, ok := engine.playbacks[next.Session]; !ok {
		t.Fatal("releasing the old plan removed the new playback")
	}
	if _, err := engine.nativeNextPlayback(context.Background(), next.Session); err == nil {
		t.Fatal("exhausted routes restarted at the beginning")
	}
	engine.nativeReleasePlayback(next.Session)
	if len(engine.playbacks) != 0 {
		t.Fatal("playback choices leaked")
	}
}

func TestNativePlaybackHLSAlternativesAreLazyAndIsolated(t *testing.T) {
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	media := providerMedia{URL: "https://first.test/index.m3u8", Playlist: "#EXTM3U\n#EXT-X-ENDLIST\n", Quality: 1080}
	media.Variants = []providerMedia{{URL: "https://backup.test.qznovelvod.com/index.m3u8", Playlist: "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-ENDLIST\n", Quality: 720}}
	plan, err := engine.nativeOpenPlayback(context.Background(), nativePlaybackChoices(media, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.stream.server.Close()
	if len(engine.stream.sessions) != 1 {
		t.Fatal("unused alternatives opened HLS sessions")
	}
	current, err := engine.nativeNextPlayback(context.Background(), plan.Session)
	if err != nil {
		t.Fatal(err)
	}
	late, err := engine.nativeNextPlayback(context.Background(), plan.Session)
	if err != nil {
		t.Fatal(err)
	}
	engine.nativeReleasePlayback(late.Session)
	engine.nativeReleasePlayback(plan.Session)
	response, err := http.Get(current.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), "#EXT-X-VERSION:3") {
		t.Fatalf("old/late cleanup broke the active HLS session: %s, %v", body, err)
	}
	engine.nativeReleasePlayback(current.Session)
	if len(engine.stream.sessions) != 0 || len(engine.playbacks) != 0 {
		t.Fatal("HLS fallback sessions leaked")
	}
}

func TestNativePlaybackCancellationAndCacheBound(t *testing.T) {
	engine := &nativeEngine{}
	choice := nativePlaybackChoices(providerMedia{URL: "https://example.test/video.mp4"}, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.nativeOpenPlayback(ctx, choice); !errors.Is(err, context.Canceled) || len(engine.playbacks) != 0 {
		t.Fatal("canceled resolution created playback state")
	}
	for i := 0; i < 20; i++ {
		if _, err := engine.nativeOpenPlayback(context.Background(), choice); err != nil {
			t.Fatal(err)
		}
	}
	if len(engine.playbacks) != 8 {
		t.Fatal("abandoned playback choices are not bounded")
	}
	for token := range engine.playbacks {
		engine.nativeReleasePlayback(token)
	}
}

// 播放会话上限**不能淘汰正在播的那一路**（0.8.13）。
//
// 首版按 `created` 挑最老的淘汰：预缓存会额外开会话，很容易把数量顶到上限，
// 一旦淘汰的是用户正在看的那一路，`nativeRelease` 会 cancel 掉它的 stream
// 会话，播放器随后请求分片拿到 410「播放已结束」—— 表现就是"看一会儿卡住，
// 退出再进才好"。这里复现该场景：先让一路真的被播放器拉过分片，
// 再把上限顶满，它必须还活着。
func TestNativePlaybackBoundKeepsSessionsBeingWatched(t *testing.T) {
	engine := &nativeEngine{}
	playing, err := engine.nativeOpenPlayback(context.Background(), nativePlaybackChoices(providerMedia{
		URL:      "https://example.test/series/index.m3u8",
		Playlist: "#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\nseg1.ts\n#EXT-X-ENDLIST\n",
	}, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.stream.server.Close()
	// 真的拉一次：会话的 `serves` 变成 1，从此算"正在播"。
	response, err := http.Get(playing.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()

	for index := 0; index < 12; index++ {
		if _, err := engine.nativeOpenPlayback(context.Background(), nativePlaybackChoices(providerMedia{
			URL: "https://example.test/other" + string(rune('a'+index)) + ".mp4",
		}, 0)); err != nil {
			t.Fatal(err)
		}
	}
	engine.mu.Lock()
	_, alive := engine.playbacks[playing.Session]
	total := len(engine.playbacks)
	engine.mu.Unlock()
	if !alive {
		t.Fatal("playing session was evicted by the playback bound")
	}
	if total > 8 {
		t.Fatalf("abandoned playbacks are not bounded: %d", total)
	}
	// 真的还能取到内容（会话没被 cancel）。
	response, err = http.Get(playing.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "#EXTM3U") {
		t.Fatalf("playing session broke: %d %s", response.StatusCode, body)
	}
}
