//go:build live

package hgv5

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestProbeVideoTabGET 验证 v5 bodyless GET video_tab 能否突破 11-20 空响应。
// 参照 jackotom/hongguo-drama 92df757 (7.7) 的 videoTabRequest。
func TestProbeVideoTabGET(t *testing.T) {
	_ = os.Setenv("HTTP_PROXY", "")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cli := &http.Client{Timeout: 12 * time.Second}

	// 注册匿名设备
	var id Identity
	ok := false
	for attempt := 0; attempt < 4; attempt++ {
		c, _ := NewIdentity()
		body, _ := BuildRegisterBody(c, time.Now())
		q, hdr := RegisterHeaders(c, body)
		regURL := "https://" + RegisterHost + PathRegister + "?" + encodeValues(q)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, regURL, bytes.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := cli.Do(req)
		if err != nil {
			t.Logf("reg %d err %v", attempt, err)
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		dev, inst, perr := ParseRegister(b)
		if perr != nil {
			t.Logf("reg %d rejected %v", attempt, perr)
			continue
		}
		c.DeviceID, c.InstallID = dev, inst
		id = c
		ok = true
		break
	}
	if !ok {
		t.Fatal("注册失败")
	}
	t.Logf("device_id=%s install_id=%s", id.DeviceID, id.InstallID)

	const vid = "7697465535398677529"
	const tabAPI = "https://api5-normal-sinfonlineb.fqnovel.com/reading/bookapi/video_tab/multi_video_model/v1/"

	for attempt := 0; attempt < 4; attempt++ {
		pairs := [][2]string{
			{"aid", "8662"}, {"app_name", "novelread"}, {"version_code", V5VersionCode}, {"version_name", V5VersionName},
			{"channel", "vivo_8662_64"}, {"device_platform", "android"}, {"device_type", "PGT-AN10"},
			{"os_version", "12"}, {"update_version_code", V5VersionCode},
			{"iid", id.InstallID}, {"cdid", id.CDID}, {"device_id", id.DeviceID},
			{"video_id", vid}, {"video_id_type", "0"}, {"device_level", "1"},
			{"need_all_video_definition", "1"}, {"need_mp4_align", "0"}, {"use_os_player", "0"},
			{"video_platform", "1024"}, {"source", "0"}, {"from_video_id", ""},
			{"_rticket", ""},
		}
		// 空 body（非 nil）→ md5Raw 计算 md5(空)，签名正确；之后删掉 body 专有头。
		empty := []byte{}
		query, headers, err := SignRequest(id, pairs, empty, time.Now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		delete(headers, "Content-Type")
		delete(headers, "x-ss-stub")
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, tabAPI+"?"+query, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		req.Header.Set("User-Agent", "com.phoenix.read/73932 (Linux; U; Android 16; zh_CN; 25053RT47C; Build/BP2A.250605.031.A3; Cronet/TTNetVersion:04657795 2026-01-23 QuicVersion:c67e9834 2025-09-08)")
		req.Header.Set("Accept", "application/json")
		resp, err := cli.Do(req)
		if err != nil {
			t.Fatalf("GET err %v", err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Logf("attempt %d HTTP %d len=%d", attempt, resp.StatusCode, len(raw))
		if len(raw) > 0 {
			var env map[string]any
			if json.Unmarshal(raw, &env) == nil {
				t.Logf("code=%v dataKeys=%v", env["code"], keysOf(env["data"]))
			}
			t.Logf("body head=%s", strconv.Quote(string(raw[:min(len(raw), 120)])))
			return
		}
		// 空结果换新设备
		fresh, _ := NewIdentity()
		nb, _ := BuildRegisterBody(fresh, time.Now())
		nq, nh := RegisterHeaders(fresh, nb)
		rreq, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+RegisterHost+PathRegister+"?"+encodeValues(nq), bytes.NewReader(nb))
		for k, v := range nh {
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
	t.Fatal("video_tab GET 始终空响应")
}

func keysOf(v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
