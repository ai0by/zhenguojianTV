package hgv5

import (
	"net/url"
	"strings"
	"testing"
)

// TestSignVideoTabGETStructure 校验 11-20 恢复路径（video_tab bodyless GET）
// 的签名结构：host/path、播放器签名参数集、以及 bodyless 时不得带 body 专有头。
// 这是纯离线测试，不依赖网络；真网验收见 video_tab_probe_test.go（build tag live）。
func TestSignVideoTabGETStructure(t *testing.T) {
	id := Identity{CDID: "cdid-1", OpenUDID: "ou", ClientUDID: "cu", DeviceID: "123456789", InstallID: "987654321"}
	const vid = "7697465535398677529"
	u, headers, err := SignVideoTabGET(id, vid, 1795158000000)
	if err != nil {
		t.Fatalf("SignVideoTabGET: %v", err)
	}

	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != VideoTabHost {
		t.Fatalf("unexpected host: %s", parsed.Host)
	}
	if parsed.Path != VideoTabPath {
		t.Fatalf("unexpected path: %s", parsed.Path)
	}
	q := parsed.Query()
	mustQuery := map[string]string{
		"aid": AID, "app_name": AppName, "version_code": V5VersionCode, "version_name": V5VersionName,
		"channel": "vivo_8662_64", "device_platform": "android", "device_type": "PGT-AN10",
		"os_version": "12", "update_version_code": V5VersionCode,
		"iid": id.InstallID, "cdid": id.CDID, "device_id": id.DeviceID,
		"video_id": vid, "video_id_type": "0", "device_level": "1",
		"need_all_video_definition": "1", "need_mp4_align": "0", "use_os_player": "0",
		"video_platform": "1024", "source": "0", "from_video_id": "",
	}
	for key, want := range mustQuery {
		if got := q.Get(key); got != want {
			t.Errorf("query %s = %q, want %q", key, got, want)
		}
	}
	if q.Get("_rticket") == "" {
		t.Error("query _rticket must be filled by signer")
	}

	// bodyless GET：必须保留播放器三大签名头，且不得带任何 body 专有头。
	for _, key := range []string{"x-khronos", "x-helios", "x-medusa"} {
		if strings.TrimSpace(headers[key]) == "" {
			t.Errorf("missing playback signature header %s", key)
		}
	}
	for _, key := range []string{"Content-Type", "x-ss-stub"} {
		if _, ok := headers[key]; ok {
			t.Errorf("bodyless GET must not carry %s", key)
		}
	}
}

// TestVideoTabBodyIsMD5Empty 验证 bodyless 签名的 body 摘要按 md5(空) 计算，
// 而不是全零（这是与带 body 请求的关键差异，也对齐 drpys 7.7 实现）。
func TestVideoTabBodyIsMD5Empty(t *testing.T) {
	sum := md5Raw([]byte{})
	const want = "d41d8cd98f00b204e9800998ecf8427e"
	if got := strings.ToLower(hexEncode(sum[:])); got != want {
		t.Fatalf("md5(empty) = %s, want %s", got, want)
	}
	var zeros [16]byte
	if sum == zeros {
		t.Fatal("md5(empty) must not be all zeros")
	}
}

func hexEncode(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&15]
	}
	return string(out)
}
