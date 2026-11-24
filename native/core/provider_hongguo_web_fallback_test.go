package core

import (
	"testing"
	"time"
)

// TestHongguoValidWebFallbackURL 校验网页片源地址白名单/过期逻辑——这是防止
// 接口被污染后把任意 URL 交给播放器/下载器的安全闸。
func TestHongguoValidWebFallbackURL(t *testing.T) {
	future := time.Now().Add(time.Hour).Unix()
	cases := []struct {
		name   string
		raw    string
		expire int64
		wantOK bool
	}{
		{"合法 qznovelvod", "https://v11-hgweb.qznovelvod.com/video/1?a=b", future, true},
		{"合法 fqnovelvod", "https://x.fqnovelvod.com/v.mp4", future, true},
		{"合法 douyinvod", "https://x.douyinvod.com/v.mp4", future, true},
		{"拒绝 http", "http://x.qznovelvod.com/v.mp4", future, false},
		{"拒绝非白名单域名", "https://evil.com/v.mp4", future, false},
		{"拒绝顶级域伪装", "https://qznovelvod.com.evil.com/v.mp4", future, false},
		{"拒绝带用户名密码", "https://user:pass@v11.qznovelvod.com/v.mp4", future, false},
		{"拒绝已过期", "https://v11.qznovelvod.com/v.mp4", time.Now().Add(-time.Hour).Unix(), false},
		{"无过期时间放行", "https://v11.qznovelvod.com/v.mp4", 0, true},
		{"拒绝空地址", "", future, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := hongguoValidWebFallbackURL(tc.raw, tc.expire)
			if ok != tc.wantOK {
				t.Fatalf("期望 ok=%v，实际 ok=%v (url=%q)", tc.wantOK, ok, got)
			}
		})
	}
}
