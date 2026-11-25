package hgv5

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSigningProfileDefaultMatchesLegacyHardcode(t *testing.T) {
	// 默认档必须与 0.8.34 的硬编码值一致，保证未下发配置时零回归。
	p := defaultSigningProfile
	if p.License != "646637794" || p.Magic != "2d4b4fca49750d433fb5ae2c226dcc56" {
		t.Fatalf("默认档 license/magic 漂移：%s %s", p.License, p.Magic)
	}
	if p.SignKey != "b23c2a470a20d105dc3f8c4438f8272ed46d6c89ffdcdedd5108698d26e9489d" {
		t.Fatal("默认档 signKey 漂移")
	}
	if p.Medusa21 != 312 || p.Path != "/novel/player/multi_video_model/player/v1" || !p.GzipBody || p.DeviceLevel != 1 || p.DrScene != "player" {
		t.Fatalf("默认档线格式漂移：%+v", p)
	}
	if p.DeviceTag7 != 0 {
		t.Fatalf("默认档不应发 device field7，当前=%d", p.DeviceTag7)
	}
}

func TestApplySigningProfileRoundTrip(t *testing.T) {
	defer SelectSigningProfile("current") // 还原默认，避免污染其它测试

	// 用 legacy 档的完整 JSON 走一遍「下发 → 校验 → 应用」。
	raw, err := json.Marshal(legacySigningProfile)
	if err != nil {
		t.Fatal(err)
	}
	var p SigningProfile
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if err := ApplySigningProfile(p); err != nil {
		t.Fatalf("应用合法档失败：%v", err)
	}
	got := ActiveSigningProfile()
	if got.Name != "v04.06.04" || got.DeviceLevel != 4 || got.GzipBody || got.Path != "/novel/player/multi_video_model/v1/" {
		t.Fatalf("档未生效：%+v", got)
	}
	if MedusaStatus() != legacySigningProfile.Medusa21 {
		t.Fatalf("medusaStatus 未同步：%d", MedusaStatus())
	}

	if err := SelectSigningProfile("current"); err != nil {
		t.Fatal(err)
	}
	if ActiveSigningProfile().Name != defaultSigningProfile.Name || MedusaStatus() != 312 {
		t.Fatal("切回默认档失败")
	}
}

func TestApplySigningProfileRejectsBadConfig(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*SigningProfile)
	}{
		{"short magic", func(p *SigningProfile) { p.Magic = "00ff" }},
		{"non-hex magic", func(p *SigningProfile) { p.Magic = strings.Repeat("zz", 16) }},
		{"short signKey", func(p *SigningProfile) { p.SignKey = "abcd" }},
		{"bad field21", func(p *SigningProfile) { p.Medusa21 = -1 }},
		{"huge field21", func(p *SigningProfile) { p.Medusa21 = 9999999 }},
		{"bad path", func(p *SigningProfile) { p.Path = "https://evil.example/x" }},
		{"bad field24", func(p *SigningProfile) { p.Field24 = "not json" }},
		{"bad device level", func(p *SigningProfile) { p.DeviceLevel = 99 }},
		{"empty name", func(p *SigningProfile) { p.Name = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := defaultSigningProfile
			tc.mut(&p)
			if err := ApplySigningProfile(p); err == nil {
				t.Fatal("坏配置应被拒绝")
			}
		})
	}
	// 坏配置被拒后当前档不应改变。
	if ActiveSigningProfile().Name != defaultSigningProfile.Name {
		t.Fatal("坏配置改动了当前档")
	}
}

func TestSelectSigningProfileUnknownFallsBack(t *testing.T) {
	defer SelectSigningProfile("current")
	if err := SelectSigningProfile("no-such-profile"); err == nil {
		t.Fatal("未知档应报错")
	}
	if err := SelectSigningProfile("v04.06.04"); err != nil {
		t.Fatal(err)
	}
	if ActiveSigningProfile().DeviceLevel != 4 {
		t.Fatal("legacy 档未生效")
	}
}
