package hgv5

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// medusaStatus 是 x-medusa 消息字段 21 —— 服务端用它判别请求来自哪套 SDK 档位，
// 会随官方版本切换（历史：377 模拟器 / 369 真机 / 312 官方 7.4.5.32）。
// 服务端只接受当前档位的值，带错值一律 HTTP 200 + 0 字节，且不区分签名对错。
//
// 做成运行时可改（atomic）有两个用途：
//  1. 远程签名档热更新：不下发代码，只切换这个状态字与同档常量；
//  2. 真网 A/B 探针：一次编译内对比多个候选值。
//
// 默认 312 与 0.8.34 一致，未配置时行为不变。
var medusaStatus atomic.Int64

func init() { medusaStatus.Store(312) }

// MedusaStatus 返回当前 x-medusa 字段 21 的状态字。
func MedusaStatus() int64 { return medusaStatus.Load() }

// SetMedusaStatus 在运行时切换 x-medusa 字段 21（热更新 / 探针用）。
func SetMedusaStatus(v int64) { medusaStatus.Store(v) }

// PlaybackV5Signer 保存客户端生命周期内稳定的进程信息；签名时只读取，可并发使用。
//
// 2026-11-25 起服务端开始校验 medusa 的完整设备/进程画像（官方 7.4.5.32 抓包口径）：
// 进程 nonce 为 25 字符（"A" + base64url(18B)），env 里还要带启动次数与 PID。
// 只发最小 envelope 时所有播放接口回 HTTP 200 + 0 字节。
type PlaybackV5Signer struct {
	started int64
	nonce   string
	launch  int64
	pid     int64
}

func NewPlaybackV5Signer(nowMillis int64) *PlaybackV5Signer {
	var raw [18]byte
	_, _ = rand.Read(raw[:])
	tok := "A" + base64.RawURLEncoding.EncodeToString(raw[:])
	var extra [2]byte
	_, _ = rand.Read(extra[:])
	return &PlaybackV5Signer{
		started: nowMillis,
		nonce:   tok[:25],
		launch:  110 + int64(extra[0]%21),
		pid:     10001 + int64(binary.BigEndian.Uint16(extra[:])%2000),
	}
}

// Sign 对实际发送的 gzip 请求体签名。查询串不添加 ts，选择分支时只调整 _rticket。
// pairs 里已有 _rticket 时就地填写（保持客户端的参数顺序），否则追加在末尾。
func (s *PlaybackV5Signer) Sign(pairs [][2]string, nowMillis int64, body []byte, entropy *Entropy) (string, map[string]string, error) {
	ts := uint32(nowMillis / 1000)
	bodyMD5 := [16]byte{}
	if body != nil {
		bodyMD5 = md5Raw(body)
	}
	withTicket := append([][2]string(nil), pairs...)
	slot := -1
	for i, kv := range withTicket {
		if kv[0] == "_rticket" {
			slot = i
			break
		}
	}
	if slot < 0 {
		withTicket = append(withTicket, [2]string{"_rticket", ""})
		slot = len(withTicket) - 1
	}
	for offset := int64(0); offset < 1024; offset++ {
		withTicket[slot][1] = strconv.FormatInt(nowMillis+offset, 10)
		query := encodeQuery(withTicket)
		queryHash := sm3Sum([]byte(query))
		iv := getIV(getIV(getIV(0x20230928, queryHash[:]), bodyMD5[:]), le32(ts))
		if iv&15 != 12 {
			continue
		}
		ent := Entropy{}
		if entropy == nil {
			ent = drawEntropy(nowMillis)
		} else {
			ent = *entropy
		}
		headers := map[string]string{
			"x-khronos": strconv.FormatUint(uint64(ts), 10),
			"x-helios":  playbackV5Helios(ts, ent.HeliosRandom),
			"x-medusa":  s.medusa(query, queryHash, bodyMD5, ts, ent),
		}
		if body != nil {
			headers["Content-Type"] = "application/json; charset=utf-8"
			headers["x-ss-stub"] = strings.ToUpper(hex.EncodeToString(bodyMD5[:]))
		}
		return query, headers, nil
	}
	return "", nil, fmt.Errorf("playback V5 signing branch unavailable")
}

func playbackV5Hash(query [32]byte, body [16]byte, ts uint32) []byte {
	const seed = uint32(0x9d4a97c2)
	count := seed + ts
	table := make([]uint32, len(branch0TT))
	for i, v := range branch0TT {
		table[i] = ror32(v, count+1)
	}
	pad := []byte{0xfa, 0x45, 0x61, 0xd7}
	for i, v := range pad {
		pad[i] = byte((uint16(v) | uint16(v)<<8) >> ((count + 2) & 7))
	}
	words := expandWords(concat(query[:], body[:], le32(ts), pad, []byte{0, 0, 0, 0, 0, 0, 1, 0xa0}))
	initial := make([]uint32, len(branch0Init))
	for i, v := range branch0Init {
		initial[i] = ror32(v, count)
	}
	state := mixRounds(append([]uint32(nil), initial...), [11]uint32{96, 1, 3, 4, 0, 6, 5, 2, 0, 7, 7}, words, table, seed)
	var result [32]byte
	for i, v := range state {
		binary.BigEndian.PutUint32(result[i*4:], v+initial[i])
	}
	for i := 0; i < 16; i++ {
		result[i] ^= result[i+16]
	}
	return append(result[:16:16], le32(sumMD5(result[:16])|0x20)...)
}

func (s *PlaybackV5Signer) medusa(query string, queryHash [32]byte, bodyMD5 [16]byte, ts uint32, entropy Entropy) string {
	p := ActiveSigningProfile()
	deviceID := queryValue(query, "device_id")
	version := queryValue(query, "version_name")
	digest := playbackV5Hash(queryHash, bodyMD5, ts)
	ctx := &envelopeContext{
		query: query, queryHash: queryHash, digest: digest, ts: ts,
		entropy: entropy, signer: s, profile: p, nowMs: time.Now().UnixMilli(),
	}

	var env, nested15, flags, extra []byte
	custom := false
	if spec := p.EnvelopeSpec; spec != nil {
		// 远程下发的声明式结构：渲染失败时静默回退到内置档，绝不产出坏签名。
		if e, n, f, x, rerr := renderCustomEnvelope(spec, ctx); rerr == nil {
			env, nested15, flags, extra, custom = e, n, f, x, true
		}
	}
	if !custom {
		_, env = s.builtinEnvelopeParts(p, ctx)
		flags = make([]byte, 8)
		nested15 = concat(protoSInt(1, 1), protoSInt(2, 1), protoSInt(3, 1), protoSInt(5, 1))
		if p.Envelope != "minimal" {
			flags, _ = hex.DecodeString("4480000000000000")
			nested15 = concat(protoSInt(1, 305), protoSInt(2, 4), protoSInt(3, 694367), protoSInt(5, 1621072674))
		}
	}
	magic := mustHex(p.Magic)
	signKey := mustHex(p.SignKey)
	message := concat(
		protoBytes(1, magic), protoSInt(2, 5), protoSInt(3, int64(entropy.MessageRandom)),
		protoString(4, appID), protoString(5, deviceID), protoString(6, p.License),
		protoString(7, version), protoString(8, p.SDKVersion), protoSInt(9, int64(p.SDKCode)),
		protoBytes(10, flags), protoSInt(12, int64(ts)), protoBytes(13, digest), protoBytes(14, queryHash[:6]),
		protoBytes(15, nested15),
	)
	if custom {
		message = append(message, extra...)
	} else if p.Envelope != "minimal" {
		message = append(message, protoString(16, s.nonce)...)
	}
	message = append(message, protoSInt(17, int64(ts))...)
	message = append(message, protoString(20, "none")...)
	message = append(message, protoSInt(21, p.Medusa21)...)
	message = append(message, protoBytes(23, env)...)
	message = append(message, protoString(24, p.Field24)...)
	hash, mask := keyHash(signKey, entropy.Random)
	transformed := append(flags, xmxor(message, hash[:])...)
	for i, j := 0, len(transformed)-1; i < j; i, j = i+1, j-1 {
		transformed[i], transformed[j] = transformed[j], transformed[i]
	}
	for i := range transformed {
		transformed[i] ^= mask[(^i)&3]
	}
	check := uint32(0x18000001) | uint32(queryHash[0]&63)<<14 | uint32(digest[0]&63)<<8
	packed := concat([]byte{0xca}, le32(entropy.PackedRandom), le32(check), transformed, []byte{byte(entropy.Random >> 16), byte(entropy.Random >> 24)})
	key := md5Raw(signKey[:16])
	iv := md5Raw(signKey[16:])
	encrypted := encryptPlaybackV5(packed, key[:], iv[:], ts)
	prefix := concat(le32(5), magic)
	for i := range prefix {
		prefix[i] ^= byte(ts >> (uint(i&3) * 8))
	}
	result := concat(prefix, []byte{byte(entropy.Random), byte(entropy.Random >> 8), 0, 1}, encrypted)
	return base64.StdEncoding.EncodeToString(result)
}

// builtinEnvelopeParts 生成内置两档（official / minimal）的 device 与 env 子消息。
// 这是 0.8.34 起一直用的硬编码结构，作为「远程配置不可用」时的兜底。
func (s *PlaybackV5Signer) builtinEnvelopeParts(p *SigningProfile, ctx *envelopeContext) (device, env []byte) {
	query := ctx.query
	deviceID := queryValue(query, "device_id")
	version := queryValue(query, "version_name")
	if p.Envelope == "minimal" {
		deviceParts := concat(
			protoSInt(1, 1), protoSInt(2, 2), protoString(3, appID), protoString(4, deviceID),
			protoString(5, s.nonce), protoString(6, "!noperm!"))
		switch p.DeviceTag7 {
		case 1:
			deviceParts = append(deviceParts, protoString(7, "!notset!")...)
		case 2:
			deviceParts = append(deviceParts, protoSInt(7, -999999)...)
		}
		device = concat(deviceParts,
			protoString(12, "Asia/Shanghai,8"),
			protoString(13, "zh_CN"), protoString(22, queryValue(query, "os_version")), protoSInt(25, s.started),
			protoString(30, queryValue(query, "device_type")), protoString(31, "HONOR"),
			protoString(32, queryValue(query, "device_type")), protoString(34, "HONOR"),
		)
		env = concat(protoString(6, p.EnvVersion), protoString(8, queryValue(query, "channel")),
			protoBytes(12, device), protoString(14, version))
		return device, env
	}
	// 官方 7.4.5.32 抓包口径：设备画像 33 个字段 + env 11 个字段。
	// 2026-11-25 起服务端按这套结构校验，缺字段一律 200 + 0 字节。
	deviceType := queryValue(query, "device_type")
	osVersion := queryValue(query, "os_version")
	brand := "HONOR"
	device = concat(
		protoSInt(1, 1), protoSInt(2, 2), protoString(3, appID), protoString(4, deviceID),
		protoString(5, s.nonce), protoString(6, "!noperm!"),
		protoSInt(7, -888888), protoSInt(8, -888888), protoSInt(9, 3), protoSInt(10, -888888),
		protoString(11, "!notset!"), protoString(12, "Asia/Shanghai,8"), protoString(13, "zh_CN"), protoSInt(14, 4),
	)
	for tag := uint32(16); tag <= 21; tag++ {
		device = append(device, protoKey(tag, 5)...)
		device = append(device, 0, 0, 0, 0)
	}
	device = concat(device,
		protoString(22, osVersion), protoSInt(23, 41), protoSInt(24, 36),
		protoSInt(25, s.started), protoSInt(26, s.started), protoSInt(27, s.started),
		protoSInt(28, ctx.nowMs),
		protoSInt(29, -1), protoString(30, deviceType), protoString(31, brand),
		protoString(32, deviceType), protoString(33, deviceType), protoString(34, brand),
		protoString(35, brand), protoString(36, brand), protoSInt(38, 31),
	)
	sub := concat(protoSInt(1, int64(ctx.ts)), protoSInt(2, -2), protoSInt(4, 200))
	env = concat(
		protoSInt(1, s.launch), protoSInt(2, 247235599), protoSInt(3, 247235596), protoSInt(5, 7),
		protoString(6, p.EnvVersion), protoSInt(7, s.pid), protoString(8, queryValue(query, "channel")),
		protoBytes(12, device), protoBytes(13, sub), protoString(14, version),
	)
	return device, env
}

// renderCustomEnvelope 用远程下发的声明式规格渲染 medusa 各段。
// 任一字段非法都返回错误，由调用方回退到内置档。
func renderCustomEnvelope(spec *EnvelopeSpec, ctx *envelopeContext) (env, nested15, flags, extra []byte, err error) {
	if err = validateEnvelopeSpec(spec); err != nil {
		return nil, nil, nil, nil, err
	}
	if spec.Flags != "" {
		flags, _ = hex.DecodeString(spec.Flags)
	} else {
		flags = make([]byte, 8)
	}
	device, err := renderEnvelopeFields(spec.Device, ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	ctx.device = device
	if env, err = renderEnvelopeFields(spec.Env, ctx); err != nil {
		return nil, nil, nil, nil, err
	}
	if nested15, err = renderEnvelopeFields(spec.Nested15, ctx); err != nil {
		return nil, nil, nil, nil, err
	}
	if extra, err = renderEnvelopeFields(spec.Message, ctx); err != nil {
		return nil, nil, nil, nil, err
	}
	return env, nested15, flags, extra, nil
}

func playbackV5Helios(ts, random uint32) string {
	seed := md5Raw(append(le32(random), []byte(appID)...))
	ascii := []byte(hex.EncodeToString(seed[:]))
	words := make([]uint64, 4)
	for i := range words {
		words[i] = binary.LittleEndian.Uint64(ascii[i*8:])
	}
	table := diffuse(words)
	text := []byte(fmt.Sprintf("%d-%s-%s", ts, ActiveSigningProfile().License, appID))
	padding := byte(16 - len(text)%16)
	for range int(padding) {
		text = append(text, padding)
	}
	out := le32(random)
	for at := 0; at < len(text); at += 16 {
		a := binary.LittleEndian.Uint64(text[at:])
		b := binary.LittleEndian.Uint64(text[at+8:])
		for _, key := range table[:34] {
			b = key ^ (a + ror64(b, 8))
			a = b ^ ror64(a, 61)
		}
		var block [16]byte
		binary.LittleEndian.PutUint64(block[:8], a)
		binary.LittleEndian.PutUint64(block[8:], b)
		out = append(out, block[:]...)
	}
	return base64.StdEncoding.EncodeToString(out)
}
