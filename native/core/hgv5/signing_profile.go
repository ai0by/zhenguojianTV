package hgv5

// 红果 v5 取流签名档（2026-11-25）。
//
// 背景：服务端会不定期切换它接受的播放签名档。历史上有两套已知档：
//   - v04.10.03（App 7.4.5.32 自带 libmetasec_ml.so；gzip body、device_level 1、/player/v1）
//   - v04.06.04（较早档；明文 body、device_level 4、/v1/、query 带 ts）
// 两套档的 license / magic / signKey / 版本串 / SDKCode / field21 / field24
// 必须**成组**切换，混搭会被静默返回 HTTP 200 + 0 字节。
//
// 本文件把这些易变常量收进 [SigningProfile]，默认档与 0.8.34 的硬编码值逐字节一致
// （零回归）；App 可在运行时通过远程配置切换到预置档或下发完整档，从而**无需重新
// 发版**即可跟随上游换档。
//
// 只作用于取流（playback_v5.go / api.go 的取流路径 / provider_hongguo_v5.go 的 gzip
// 头）。目录 / 详情 / 搜索 / 弹幕走通用签名（medusa.go / gorgon.go），当前正常，
// 刻意不动。

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
)

// SigningProfile 是一套完整的 v5 取流线格式参数。字段改动必须成组进行。
type SigningProfile struct {
	Name string `json:"name"`

	// —— 签名层（x-medusa / x-helios / x-gorgon）——
	License    string `json:"license"`    // x-medusa field 6，也是 x-helios 输入
	Magic      string `json:"magic"`      // hex，x-medusa field 1 + 密文前缀
	SignKey    string `json:"signKey"`    // hex，keyHash / AES key+iv 的种子
	SDKVersion string `json:"sdkVersion"` // x-medusa field 8，如 v04.10.03-ml-android
	EnvVersion string `json:"envVersion"` // env field 6，如 v04.10.03.04-bugfix
	SDKCode    uint32 `json:"sdkCode"`    // x-medusa field 9，也进 x-gorgon RC4
	GorgonHi   byte   `json:"gorgonHi"`   // x-gorgon 密钥第 2 字节 / result 第 5 字节
	GorgonLo   byte   `json:"gorgonLo"`   // x-gorgon 密钥第 7 字节 / result 第 6 字节
	Medusa21   int64  `json:"medusa21"`   // x-medusa field 21 状态字（历史 377/369/312）
	Field24    string `json:"field24"`    // x-medusa field 24 的 JSON 字符串
	// DeviceTag7: 0 = 不发（minimal 档原样）；1 = 发 "!notset!"；2 = 发整数 -999999。
	// official envelope 固定发 -888888，忽略本字段。
	DeviceTag7 int `json:"deviceTag7"`
	// Envelope 决定 medusa 明文的完整度：
	//   "" / "official" = 官方 7.4.5.32 完整画像（33 字段设备 + 11 字段 env，
	//                     flags 4480000000000000、field15 305/4/694367/1621072674、
	//                     field16 进程 nonce）。2026-11-25 起服务端只认这套。
	//   "minimal"       = 0.8.34 的极简结构，仅用于 A/B 对照与回滚。
	Envelope string `json:"envelope"`
	// EnvelopeSpec 非空时优先于 Envelope，用声明式字段表渲染 medusa 明文。
	// 这是「结构级热更新」的入口：上游加字段 / 改字段值 / 改 flags 时，
	// 只需下发新的 spec，不必发版。字段说明见 envelope_spec.go。
	EnvelopeSpec *EnvelopeSpec `json:"envelopeSpec,omitempty"`

	// —— 线格式层（路径 / body）——
	Path        string `json:"path"`        // 播放模型路径
	GzipBody    bool   `json:"gzipBody"`    // body 是否 gzip（并带 Content-Encoding）
	DeviceLevel int    `json:"deviceLevel"` // body biz_param.device_level
	DrScene     string `json:"drScene"`     // 非空时写进 body 的 dr_scene
	QueryTS     bool   `json:"queryTs"`     // query 是否带 ts
}

// 默认档：与 0.8.34 取流实现逐字节一致（gorgon 0x40/0x01、medusa21=312、
// field24 的 un_h=11、license 646637794）。这样未下发远程配置时行为完全不变。
var defaultSigningProfile = SigningProfile{
	Name:        "current",
	License:     "646637794",
	Magic:       "2d4b4fca49750d433fb5ae2c226dcc56",
	SignKey:     "b23c2a470a20d105dc3f8c4438f8272ed46d6c89ffdcdedd5108698d26e9489d",
	SDKVersion:  "v04.10.03-ml-android",
	EnvVersion:  "v04.10.03.04-bugfix",
	SDKCode:     0x040a0300,
	GorgonHi:    0x40,
	GorgonLo:    0x01,
	Medusa21:    312,
	Field24:     `{"cmr":16777216,"cmr2":16777216,"un_h":11,"vpn":0,"kd":0,"dyn":"","do":1,"tk":true}`,
	DeviceTag7:  0,
	Envelope:    "official", // 2026-11-25 起服务端只认完整画像
	Path:        "/novel/player/multi_video_model/player/v1",
	GzipBody:    true,
	DeviceLevel: 1,
	DrScene:     "player",
}

// 预置档：openhongguo 双档机制的 v04.06.04 档（完整成组切换）。
// 注意 gorgon flags 用该档的 0x40/0x01（与 openhongguo PR#23 一致）。
var legacySigningProfile = SigningProfile{
	Name:        "v04.06.04",
	License:     "AXYQOS6n2m60x1fVZHIrH3iol",
	Magic:       "f7e85ffad7d7dc3bd62ac87057cf6118",
	SignKey:     "8ebdfa3806ecc5cee79423e6029ed82540bc2218bb7eaef71cb691f7aa8aa2f5",
	SDKVersion:  "v04.06.04-ml-android",
	EnvVersion:  "v04.06.04.03-bugfix",
	SDKCode:     0x04060400,
	GorgonHi:    0x40,
	GorgonLo:    0x01,
	Medusa21:    312,
	Field24:     `{"cmr":16777216,"cmr2":16777216,"un_h":1879194040,"vpn":0,"kd":0,"fkd":3672518972,"pd":-1872573247,"dyn":"","do":0,"tk":true}`,
	DeviceTag7:  1, // v04.06.04 档 device block 补 "!notset!"
	Path:        "/novel/player/multi_video_model/v1/",
	GzipBody:    false,
	DeviceLevel: 4,
	DrScene:     "",
	QueryTS:     true,
}

// activeSigningProfile 是运行时生效的档，atomic.Pointer 保证并发签名读到一致快照。
var activeSigningProfile atomic.Pointer[SigningProfile]

func init() {
	p := defaultSigningProfile
	activeSigningProfile.Store(&p)
}

// ActiveSigningProfile 返回当前生效档（只读，不要修改返回值）。
func ActiveSigningProfile() *SigningProfile { return activeSigningProfile.Load() }

// ApplySigningProfile 校验并应用一套档。校验失败返回错误，不改动当前档。
func ApplySigningProfile(p SigningProfile) error {
	if err := validateSigningProfile(p); err != nil {
		return err
	}
	activeSigningProfile.Store(&p)
	// medusaStatus 保留为兼容入口：让旧探针/旧调用读到的值同步更新。
	SetMedusaStatus(p.Medusa21)
	return nil
}

// SelectSigningProfile 按名字切到预置档。"current"/"" 回默认档。
func SelectSigningProfile(name string) error {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "current", "default":
		return ApplySigningProfile(defaultSigningProfile)
	case "v04.06.04", "legacy":
		return ApplySigningProfile(legacySigningProfile)
	case "v04.10.03":
		return ApplySigningProfile(defaultSigningProfile)
	default:
		return fmt.Errorf("未知签名档 %q", name)
	}
}

// validateSigningProfile 防呆：拒绝会被静默丢弃或引入风险的坏配置。
// 只做结构/范围校验，不接触任何凭据。
func validateSigningProfile(p SigningProfile) error {
	if len(p.Name) == 0 || len(p.Name) > 40 {
		return errors.New("签名档名称无效")
	}
	if !isHex(p.Magic, 16) {
		return errors.New("签名档 magic 必须是 16 字节 hex")
	}
	if !isHex(p.SignKey, 32) {
		return errors.New("签名档 signKey 必须是 32 字节 hex")
	}
	if p.License == "" || len(p.License) > 64 {
		return errors.New("签名档 license 无效")
	}
	if p.SDKVersion == "" || len(p.SDKVersion) > 64 || p.EnvVersion == "" || len(p.EnvVersion) > 64 {
		return errors.New("签名档版本串无效")
	}
	if p.SDKCode == 0 {
		return errors.New("签名档 SDKCode 无效")
	}
	if p.Medusa21 < 1 || p.Medusa21 > 100000 {
		return errors.New("签名档 field21 超出范围")
	}
	if !validField24(p.Field24) {
		return errors.New("签名档 field24 必须是合法 JSON 对象")
	}
	if p.Path == "" || !strings.HasPrefix(p.Path, "/novel/player/") || len(p.Path) > 120 {
		return errors.New("签名档路径无效")
	}
	if p.DeviceLevel < 0 || p.DeviceLevel > 9 {
		return errors.New("签名档 device_level 超出范围")
	}
	switch p.Envelope {
	case "", "official", "minimal", "custom":
	default:
		return errors.New("签名档 envelope 只能是 official / minimal / custom")
	}
	if p.EnvelopeSpec != nil {
		if err := validateEnvelopeSpec(p.EnvelopeSpec); err != nil {
			return err
		}
	}
	return nil
}

func isHex(s string, bytes int) bool {
	if len(s) != bytes*2 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// validField24 只要求是以 { 开头、} 结尾的短 JSON 对象，避免塞入超长/注入内容。
func validField24(s string) bool {
	s = strings.TrimSpace(s)
	return len(s) > 2 && len(s) <= 2048 && strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")
}

// 保留 strconv 引用：field24 里的数值由上游以文本下发，这里提供解析辅助给测试用。
var _ = strconv.Itoa
