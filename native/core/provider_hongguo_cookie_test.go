package core

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHongguoNormalizeCookie(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"整行头", "Cookie: sessionid=abc; ttwid=xyz", "sessionid=abc; ttwid=xyz"},
		{"小写头", "cookie: sessionid=abc", "sessionid=abc"},
		{"多行", "sessionid=abc;\nttwid=xyz\n", "sessionid=abc; ttwid=xyz"},
		{"空", "   \n  ", ""},
	}
	for _, item := range cases {
		if got := hongguoNormalizeCookie(item.raw); got != item.want {
			t.Fatalf("%s：want %q, got %q", item.name, item.want, got)
		}
	}
	// 超长输入必须被截断，避免把整段请求头塞进来。
	long := strings.Repeat("a=1; ", 4000)
	if got := hongguoNormalizeCookie(long); len(got) > hongguoCookieMaxLength {
		t.Fatalf("超长 Cookie 未截断：%d", len(got))
	}
}

// 0.8.14：配置登录态后，网页播放页要带上 Cookie，并从 videoList 取真实片源。
func TestHongguoWebMediaUsesLoginCookie(t *testing.T) {
	seriesID, videoID := "7605500183484320792", "7605502162419846206"
	var seenCookie string
	body := hongguoPlayerPageFixture(seriesID, videoID, `"videoList": {"1080p": {"main_url": "https://v5-reading-video-d.qznovelvod.com/a/full.mp4", "duration": "147.5"}}`)
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		seenCookie = request.Header.Get("Cookie")
		return sourceFixtureResponse(request, http.StatusOK, body), nil
	})
	d.setHongguoCookie("sessionid=abc; ttwid=xyz")

	media, err := d.resolveHongguoWebMedia(context.Background(), seriesID, videoID)
	if err != nil {
		t.Fatalf("带登录态应成功，实际 err=%v", err)
	}
	if seenCookie != "sessionid=abc; ttwid=xyz" {
		t.Fatalf("请求未带 Cookie：%q", seenCookie)
	}
	if media.URL != "https://v5-reading-video-d.qznovelvod.com/a/full.mp4" {
		t.Fatalf("片源地址不对：%q", media.URL)
	}
	if media.Duration < 147*time.Second || media.Duration > 148*time.Second {
		t.Fatalf("时长解析不对：%v", media.Duration)
	}
}

// 没配置登录态时，要明确告诉用户去哪里配置，而不是含糊地失败。
func TestHongguoWebMediaHintsLoginWhenNoCookie(t *testing.T) {
	seriesID, videoID := "7605500183484320792", "7605502162419846206"
	body := hongguoPlayerPageFixture(seriesID, videoID, `"videoList": {}`)
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusOK, body), nil
	})

	_, err := d.resolveHongguoWebMedia(context.Background(), seriesID, videoID)
	if err == nil || !strings.Contains(err.Error(), "设置") {
		t.Fatalf("应提示去设置里配置登录态，实际 err=%v", err)
	}
}

// 就算给了地址，只有 10 秒也要拒收（登录态失效时站点会退回预告）。
func TestHongguoWebMediaRejectsPreviewDuration(t *testing.T) {
	seriesID, videoID := "7605500183484320792", "7605502162419846206"
	body := hongguoPlayerPageFixture(seriesID, videoID, `"videoList": {"360p": {"main_url": "https://hongguoduanju.com/video/public/7605502162419846206/360p.mp4", "duration": "10.0"}}`)
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusOK, body), nil
	})
	d.setHongguoCookie("sessionid=abc")

	_, err := d.resolveHongguoWebMedia(context.Background(), seriesID, videoID)
	if err == nil || !strings.Contains(err.Error(), "预告") {
		t.Fatalf("10 秒片源应被拒，实际 err=%v", err)
	}
}

// hongguoPlayerPageFixture 造一个最小的红果播放页（_ROUTER_DATA）。
func hongguoPlayerPageFixture(seriesID, videoID, extra string) string {
	page := fmt.Sprintf(`{"vid": %q, "series_id": %q, "protectedPlayback": false, %s}`, videoID, seriesID, extra)
	return `<html><body><script>_ROUTER_DATA = {"loaderData": {"player_page": ` + page + `}}</script></body></html>`
}
