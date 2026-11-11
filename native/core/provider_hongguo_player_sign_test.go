package core

// 播放器专用签名的回归测试（2026-11-11）。
//
// 上游对取流接口收紧了签名校验：通用签名（signHongguoAppRequest，自带
// X-Medusa / X-Helios）会拿到 200 + 0 字节空正文，只有播放器专用签名能出片。
// 这里把「签名头形态」与「实际发出的取流请求用哪套签名」都钉死，避免以后
// 有人为了「双保险」把 signHongguoAppRequest 加回来 —— 叠加反而会打回空正文。

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"
)

const hongguoPlayerSignTestQuery = "aid=8662&device_id=76338817222425&video_id=7605502162419846206&_rticket=1760000000000"

func hongguoPlayerSignTestRequest(t *testing.T, query string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet,
		"https://api5-normal-sinfonlineb.fqnovel.com/reading/bookapi/video_tab/multi_video_model/v1/?"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

// Gorgon 不含随机 nonce，只由 query / body / 时间戳决定，可重复且必须三者都覆盖。
func TestHongguoPlayerSignGorgonIsDeterministicAndBoundToRequest(t *testing.T) {
	stamp := time.Unix(1760000000, 0)
	body := []byte(`{"video_id":"7605502162419846206"}`)
	sign := func(query string, payload []byte, now time.Time) string {
		request := hongguoPlayerSignTestRequest(t, query)
		signHongguoPlayerRequest(request, payload, now)
		return request.Header.Get("X-Gorgon")
	}

	first := sign(hongguoPlayerSignTestQuery, body, stamp)
	if again := sign(hongguoPlayerSignTestQuery, body, stamp); again != first {
		t.Fatalf("同一 query/body/时间应生成同一 Gorgon：%s vs %s", first, again)
	}
	// 6 字节固定头 + 20 字节 payload = 26 字节 → 52 个 hex 字符。
	decoded, err := hex.DecodeString(first)
	if err != nil || len(decoded) != 26 {
		t.Fatalf("Gorgon 应是 26 字节 hex，实际 %q（err=%v）", first, err)
	}
	// 播放器专用签名的固定头；通用签名是 8404{random}{random}4001，一眼可分。
	if got := hex.EncodeToString(decoded[:6]); got != "8404401c0000" {
		t.Fatalf("Gorgon 头不是播放器专用版本：%s", got)
	}
	if changed := sign(hongguoPlayerSignTestQuery+"&extra=1", body, stamp); changed == first {
		t.Fatal("Gorgon 没有覆盖 query（改了 _rticket 就会整发被拒）")
	}
	if changed := sign(hongguoPlayerSignTestQuery, []byte(`{"video_id":"other"}`), stamp); changed == first {
		t.Fatal("Gorgon 没有覆盖 body")
	}
	if changed := sign(hongguoPlayerSignTestQuery, body, stamp.Add(time.Second)); changed == first {
		t.Fatal("Gorgon 没有覆盖时间戳")
	}
}

func TestHongguoPlayerSignHeadersExcludeGeneralSignExtras(t *testing.T) {
	stamp := time.Unix(1760000000, 123000000)
	request := hongguoPlayerSignTestRequest(t, hongguoPlayerSignTestQuery)
	signHongguoPlayerRequest(request, nil, stamp)

	if got := request.Header.Get("X-Khronos"); got != "1760000000" {
		t.Fatalf("X-Khronos 不对：%q", got)
	}
	if got := request.Header.Get("X-SS-Req-Ticket"); got != "1760000000123" {
		t.Fatalf("X-SS-Req-Ticket 不对：%q", got)
	}
	// GET video_tab 没有 body，不该带 X-SS-STUB。
	if got := request.Header.Get("X-SS-Stub"); got != "" {
		t.Fatalf("无 body 时不应设置 X-SS-Stub，实际 %q", got)
	}

	argus, err := base64.StdEncoding.DecodeString(request.Header.Get("X-Argus"))
	if err != nil || len(argus) < 18 {
		t.Fatalf("X-Argus 不是合法 base64 密文：len=%d err=%v", len(argus), err)
	}
	if argus[0] != 0x3c || argus[1] != 0xcc || (len(argus)-2)%16 != 0 {
		t.Fatalf("X-Argus 结构不对：前缀 %x 长度 %d", argus[:2], len(argus))
	}
	ladon, err := base64.StdEncoding.DecodeString(request.Header.Get("X-Ladon"))
	if err != nil || len(ladon) < 20 {
		t.Fatalf("X-Ladon 不是合法 base64 密文：len=%d err=%v", len(ladon), err)
	}
	// 前 4 字节是 nonce 前缀，其后必须是 16 字节对齐的分组密文。
	if (len(ladon)-4)%16 != 0 {
		t.Fatalf("X-Ladon 密文未按 16 字节对齐：长度 %d", len(ladon))
	}

	// 最关键的一条：这些通用签名的头一旦出现在取流请求上，服务端就回空正文。
	for _, key := range []string{
		"X-Medusa", "X-Helios", "X-Tt-Request-Tag",
		"Passport-Sdk-Version", "X-Vc-Bdturing-Sdk-Version",
	} {
		if got := request.Header.Get(key); got != "" {
			t.Fatalf("播放器签名不应设置 %s：%q", key, got)
		}
	}
}

// 端到端夹具：真实走一遍 resolveHongguoAppMedia，确认取流请求带的是播放器
// 签名而不是通用签名，且仍能解析出可播地址（防止签名被换错、解析链被带偏）。
func TestHongguoPlaybackRequestsUsePlayerSignature(t *testing.T) {
	const videoID = "7694655440692448281"
	body := hongguoModelBody(t, videoID, map[string]any{
		"status":         10,
		"video_duration": 79.534,
		"video_list": []any{map[string]any{
			"main_url":   "https://v5-reading-video-d.qznovelvod.com/a/1080p.mp4",
			"video_meta": map[string]any{"codec_type": "h264", "vheight": 1920},
		}},
	})
	var captured []http.Header
	d := sourceFixtureDownloader(t, withHongguoPlaybackRegistration(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "multi_video_model") {
			captured = append(captured, request.Header.Clone())
			return sourceFixtureResponse(request, http.StatusOK, body), nil
		}
		return hongguoProbeResponse(request, 79*time.Second), nil
	}))

	media, err := d.resolveHongguoAppMedia(context.Background(), videoID)
	if err != nil {
		t.Fatalf("取流应成功：%v", err)
	}
	if !strings.HasSuffix(media.URL, "1080p.mp4") {
		t.Fatalf("解析出的地址不对：%s", media.URL)
	}
	if len(captured) == 0 {
		t.Fatal("没抓到取流请求")
	}
	for index, header := range captured {
		if gorgon := header.Get("X-Gorgon"); !strings.HasPrefix(gorgon, "8404401c0000") {
			t.Fatalf("第 %d 发取流请求用的不是播放器签名：%q", index, gorgon)
		}
		if header.Get("X-Argus") == "" || header.Get("X-Ladon") == "" {
			t.Fatalf("第 %d 发取流请求缺 X-Argus / X-Ladon", index)
		}
		if header.Get("X-Medusa") != "" || header.Get("X-Helios") != "" {
			t.Fatalf("第 %d 发取流请求叠加了通用签名头，上游会回空正文", index)
		}
	}
}
