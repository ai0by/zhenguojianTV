//go:build live

package hgv5

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestLiveFetch 真网验证 v5 注册 + 取流整链（默认跳过，需 -tags live）。
//
//	go test -tags live -run TestLiveFetch -v -count=1 ./core/hgv5
//
// episodeID 用 2026-11-19 实测可播那一集；断言取到非空 video_model、
// 片源为 hvc1/bytevc1（可播）而非 bytevc2。
func TestLiveFetch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cli := &http.Client{Timeout: 15 * time.Second}
	const episodeID = "7697465535398677529"

	// 1) 注册（tt-data 信封）。最多换 4 台设备，空结果就重来。
	var id Identity
	var raw []byte
	ok := false
	for attempt := 0; attempt < 4; attempt++ {
		candidate, err := NewIdentity()
		if err != nil {
			t.Fatal(err)
		}
		body, err := BuildRegisterBody(candidate, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		q, hdr := RegisterHeaders(candidate, body)
		regURL := "https://" + RegisterHost + PathRegister + "?" + encodeValues(q)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, regURL, bytes.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := cli.Do(req)
		if err != nil {
			t.Logf("register attempt %d err: %v", attempt, err)
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		dev, inst, perr := ParseRegister(b)
		if perr != nil {
			t.Logf("register attempt %d rejected: %v (%s)", attempt, perr, string(b[:min(len(b), 120)]))
			continue
		}
		candidate.DeviceID, candidate.InstallID = dev, inst
		id = candidate
		ok = true
		break
	}
	if !ok {
		t.Fatal("无法注册匿名设备（4 次都失败）")
	}
	t.Logf("registered device_id=%s install_id=%s cdid=%s", id.DeviceID, id.InstallID, id.CDID)

	// 2) 取流。空结果换设备重签（这里直接换一台重新注册再取）。
	for attempt := 0; attempt < 4; attempt++ {
		body, err := BuildPlaybackBody(episodeID)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UnixMilli()
		query, headers, err := SignRequest(id, PlaybackPairs(id), body, now)
		if err != nil {
			t.Fatal(err)
		}
		u := APIBase + PathPlayback + "?" + query
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		req.Header.Set("User-Agent", PlaybackUserAgent())
		req.Header.Set("Accept", "application/json; charset=utf-8,application/x-protobuf")
		req.Header.Set("x-xs-from-web", "0")
		req.Header.Set("Content-Encoding", "gzip")
		resp, err := cli.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Logf("playback attempt %d: HTTP %d len=%d", attempt, resp.StatusCode, len(raw))
		if len(raw) > 0 {
			break
		}
		// 空结果 → 换设备重新注册
		fresh, err := NewIdentity()
		if err != nil {
			t.Fatal(err)
		}
		nb, _ := BuildRegisterBody(fresh, time.Now())
		q, hdr := RegisterHeaders(fresh, nb)
		rreq, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+RegisterHost+PathRegister+"?"+encodeValues(q), bytes.NewReader(nb))
		for k, v := range hdr {
			rreq.Header.Set(k, v)
		}
		rresp, err := cli.Do(rreq)
		if err != nil {
			t.Fatal(err)
		}
		rb, _ := io.ReadAll(rresp.Body)
		rresp.Body.Close()
		dev, inst, perr := ParseRegister(rb)
		if perr != nil {
			t.Fatal(perr)
		}
		fresh.DeviceID, fresh.InstallID = dev, inst
		id = fresh
	}
	if len(raw) == 0 {
		t.Fatal("取流始终返回空正文")
	}

	// 3) 断言响应里有 video_list 且 codec 不是 bytevc2。
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("响应不是 JSON: %v", err)
	}
	vmRaw := env.Data["video_model"]
	if len(vmRaw) == 0 {
		// v5 取流把 model 放在 data.<episodeID>.video_model
		if entry, ok := env.Data[episodeID]; ok {
			var e map[string]json.RawMessage
			if json.Unmarshal(entry, &e) == nil {
				vmRaw = e["video_model"]
			}
		}
	}
	if len(vmRaw) == 0 {
		t.Fatalf("无 video_model: %s", string(raw[:min(len(raw), 200)]))
	}
	var vm map[string]any
	if vmRaw[0] == '"' {
		var s string
		json.Unmarshal(vmRaw, &s)
		vmRaw = []byte(s)
	}
	if err := json.Unmarshal(vmRaw, &vm); err != nil {
		t.Fatal(err)
	}
	list, _ := vm["video_list"].([]any)
	if len(list) == 0 {
		t.Fatal("video_model 无 video_list")
	}
	t.Logf("video_list 档位数: %d", len(list))
	bytevc2 := 0
	for _, row := range list {
		r, _ := row.(map[string]any)
		codec := ""
		if meta, ok := r["video_meta"].(map[string]any); ok {
			codec, _ = meta["codec_type"].(string)
		}
		if codec == "" {
			codec, _ = r["codec_type"].(string)
		}
		if strings.Contains(strings.ToLower(codec), "bvc2") {
			bytevc2++
		}
	}
	if bytevc2 == len(list) {
		t.Fatalf("全部档位都是 bytevc2（不可播）")
	}
	t.Logf("OK: 取到 %d 档，bytevc2=%d（存在可播档位）", len(list), bytevc2)
}

func encodeValues(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
	}
	return strings.Join(parts, "&")
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
