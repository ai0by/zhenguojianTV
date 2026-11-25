package hgv5

// 声明式 x-medusa envelope（2026-11-25）。
//
// 背景：0.8.34 那套 medusa 明文是硬编码的极简结构。2026-11-25 上游改成按官方
// 7.4.5.32 完整画像校验后，所有取流接口回 HTTP 200 + 0 字节 —— 缺的不是某个常量，
// 而是十几个 protobuf 字段。这类变化（加字段 / 改字段值 / 改 flags / 改嵌套）以后
// 还会发生，靠发版跟进不可持续。
//
// 本文件把 medusa 明文的「字段序列」抽成可远程下发的 [EnvelopeSpec]：
// 每个字段只声明 tag + 一个值来源，由 [renderEnvelope] 渲染成 protobuf。
// 只要上游改的是**结构或取值**（这是绝大多数情况），改远程 JSON 即可生效；
// 只有换哈希/AES 等**算法**时才必须发版（算法不在可下发范围内，见 README 说明）。
//
// 安全边界（刻意收窄）：
//   - 只能产出 protobuf 字段，不能执行任意代码、不能发起额外请求；
//   - tag 限定 1..65535，值来源必须且只能有一个；
//   - 渲染结果长度上限 8KB，字段数上限 256，嵌套深度上限 4；
//   - 校验失败一律拒绝，继续用当前生效档（不会把 App 弄成完全不可用）。

import (
	"encoding/hex"
	"fmt"
)

// EnvelopeField 是 medusa 明文里的一个 protobuf 字段。
//
// 值来源九选一（其余留空）：Int / Str / Hex / Fixed32 / Float / Query / Proc / Msg / Nested。
type EnvelopeField struct {
	Tag uint32 `json:"t"`

	Int     *int64   `json:"i,omitempty"` // sint（zigzag varint）
	Str     *string  `json:"s,omitempty"` // UTF-8 字符串
	Hex     string   `json:"h,omitempty"` // hex 字节串
	Fixed32 *uint32  `json:"x,omitempty"` // fixed32（wire 5）
	Float   *float32 `json:"f,omitempty"` // float（wire 5）

	Query  string          `json:"q,omitempty"` // 取 query 参数值（字符串）
	Proc   string          `json:"p,omitempty"` // 进程/时间量：nonce|launch|pid|started|now
	Msg    string          `json:"m,omitempty"` // 消息量：ts|digest|query6|rand|license|version|magic|sdkcode|aid|deviceid|device|env
	Nested []EnvelopeField `json:"n,omitempty"` // 嵌套子消息
}

// EnvelopeSpec 描述一整条 medusa 明文的组装方式。
type EnvelopeSpec struct {
	Flags    string          `json:"flags,omitempty"`    // field10 的 8 字节，hex；空则全零
	Device   []EnvelopeField `json:"device,omitempty"`   // env.field12 的设备画像
	Env      []EnvelopeField `json:"env,omitempty"`      // env 子消息
	Nested15 []EnvelopeField `json:"nested15,omitempty"` // message.field15
	Message  []EnvelopeField `json:"message,omitempty"`  // 追加到 message 尾部的额外字段
}

const (
	envelopeMaxFields = 256
	envelopeMaxDepth  = 4
	envelopeMaxBytes  = 8192
)

// envelopeContext 是渲染时可用到的全部外部量。
type envelopeContext struct {
	query     string
	queryHash [32]byte
	digest    []byte
	ts        uint32
	entropy   Entropy
	signer    *PlaybackV5Signer
	profile   *SigningProfile
	nowMs     int64
	device    []byte // 渲染 env 时填充，供 field12 引用
}

// validateEnvelopeSpec 静态校验一份规格，不产生任何副作用。
func validateEnvelopeSpec(spec *EnvelopeSpec) error {
	if spec == nil {
		return fmt.Errorf("envelope 规格为空")
	}
	if spec.Flags != "" {
		raw, err := hex.DecodeString(spec.Flags)
		if err != nil || len(raw) != 8 {
			return fmt.Errorf("envelope flags 必须是 16 位 hex")
		}
	}
	if len(spec.Device) == 0 && len(spec.Env) == 0 {
		return fmt.Errorf("envelope 规格缺少 device/env")
	}
	groups := map[string][]EnvelopeField{
		"device": spec.Device, "env": spec.Env,
		"nested15": spec.Nested15, "message": spec.Message,
	}
	for name, fields := range groups {
		if err := validateEnvelopeFields(fields, name, 1); err != nil {
			return err
		}
	}
	return nil
}

func validateEnvelopeFields(fields []EnvelopeField, where string, depth int) error {
	if depth > envelopeMaxDepth {
		return fmt.Errorf("envelope 嵌套超过 %d 层", envelopeMaxDepth)
	}
	if len(fields) > envelopeMaxFields {
		return fmt.Errorf("envelope %s 字段过多", where)
	}
	for _, f := range fields {
		if f.Tag == 0 || f.Tag > 65535 {
			return fmt.Errorf("envelope %s 字段 tag 越界: %d", where, f.Tag)
		}
		sources := 0
		if f.Int != nil {
			sources++
		}
		if f.Str != nil {
			sources++
		}
		if f.Hex != "" {
			sources++
		}
		if f.Fixed32 != nil {
			sources++
		}
		if f.Float != nil {
			sources++
		}
		if f.Query != "" {
			sources++
		}
		if f.Proc != "" {
			sources++
		}
		if f.Msg != "" {
			sources++
		}
		if f.Nested != nil {
			sources++
		}
		if sources != 1 {
			return fmt.Errorf("envelope %s 字段 %d 必须且只能有一个值来源", where, f.Tag)
		}
		if f.Proc != "" && !validProcSource(f.Proc) {
			return fmt.Errorf("envelope 未知进程量: %s", f.Proc)
		}
		if f.Msg != "" && !validMsgSource(f.Msg) {
			return fmt.Errorf("envelope 未知消息量: %s", f.Msg)
		}
		if f.Nested != nil {
			if err := validateEnvelopeFields(f.Nested, where, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func validProcSource(name string) bool {
	switch name {
	case "nonce", "launch", "pid", "started", "now":
		return true
	}
	return false
}

func validMsgSource(name string) bool {
	switch name {
	case "ts", "digest", "query6", "rand", "license", "version", "magic",
		"sdkcode", "aid", "deviceid", "device", "env":
		return true
	}
	return false
}

// renderEnvelopeFields 把字段序列渲染成 protobuf 字节。
func renderEnvelopeFields(fields []EnvelopeField, ctx *envelopeContext) ([]byte, error) {
	out := make([]byte, 0, 256)
	for _, f := range fields {
		part, err := renderEnvelopeField(f, ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
		if len(out) > envelopeMaxBytes {
			return nil, fmt.Errorf("envelope 渲染结果超过 %d 字节", envelopeMaxBytes)
		}
	}
	return out, nil
}

func renderEnvelopeField(f EnvelopeField, ctx *envelopeContext) ([]byte, error) {
	switch {
	case f.Int != nil:
		return protoSInt(f.Tag, *f.Int), nil
	case f.Str != nil:
		return protoString(f.Tag, *f.Str), nil
	case f.Hex != "":
		raw, err := hex.DecodeString(f.Hex)
		if err != nil {
			return nil, fmt.Errorf("envelope 字段 %d 的 hex 无效", f.Tag)
		}
		return protoBytes(f.Tag, raw), nil
	case f.Fixed32 != nil:
		out := protoKey(f.Tag, 5)
		return append(out, le32(*f.Fixed32)...), nil
	case f.Float != nil:
		return protoFloat(f.Tag, *f.Float), nil
	case f.Query != "":
		return protoString(f.Tag, queryValue(ctx.query, f.Query)), nil
	case f.Proc != "":
		return renderProcSource(f.Tag, f.Proc, ctx)
	case f.Msg != "":
		return renderMsgSource(f.Tag, f.Msg, ctx)
	case f.Nested != nil:
		body, err := renderEnvelopeFields(f.Nested, ctx)
		if err != nil {
			return nil, err
		}
		return protoBytes(f.Tag, body), nil
	}
	return nil, fmt.Errorf("envelope 字段 %d 没有值来源", f.Tag)
}

func renderProcSource(tag uint32, name string, ctx *envelopeContext) ([]byte, error) {
	switch name {
	case "nonce":
		return protoString(tag, ctx.signer.nonce), nil
	case "launch":
		return protoSInt(tag, ctx.signer.launch), nil
	case "pid":
		return protoSInt(tag, ctx.signer.pid), nil
	case "started":
		return protoSInt(tag, ctx.signer.started), nil
	case "now":
		return protoSInt(tag, ctx.nowMs), nil
	}
	return nil, fmt.Errorf("envelope 未知进程量: %s", name)
}

func renderMsgSource(tag uint32, name string, ctx *envelopeContext) ([]byte, error) {
	switch name {
	case "ts":
		return protoSInt(tag, int64(ctx.ts)), nil
	case "digest":
		return protoBytes(tag, ctx.digest), nil
	case "query6":
		return protoBytes(tag, ctx.queryHash[:6]), nil
	case "rand":
		return protoSInt(tag, int64(ctx.entropy.MessageRandom)), nil
	case "license":
		return protoString(tag, ctx.profile.License), nil
	case "version":
		return protoString(tag, queryValue(ctx.query, "version_name")), nil
	case "magic":
		return protoBytes(tag, mustHex(ctx.profile.Magic)), nil
	case "sdkcode":
		return protoSInt(tag, int64(ctx.profile.SDKCode)), nil
	case "aid":
		return protoString(tag, appID), nil
	case "deviceid":
		return protoString(tag, queryValue(ctx.query, "device_id")), nil
	case "device":
		if ctx.device == nil {
			return nil, fmt.Errorf("envelope device 尚未渲染")
		}
		return protoBytes(tag, ctx.device), nil
	case "env":
		return nil, fmt.Errorf("envelope env 只能由主流程渲染")
	}
	return nil, fmt.Errorf("envelope 未知消息量: %s", name)
}

// officialEnvelopeSpec 是官方 7.4.5.32 的完整画像，与 medusa() 里 official 分支
// 逐字节等价（由测试保证）。它同时充当远程下发时的参考模板：
// 上游若只是加字段，复制本结构改几行即可。
func officialEnvelopeSpec() *EnvelopeSpec {
	sint := func(v int64) *int64 { return &v }
	str := func(v string) *string { return &v }
	u32 := func(v uint32) *uint32 { return &v }
	zero := u32(0)
	return &EnvelopeSpec{
		Flags: "4480000000000000",
		Device: []EnvelopeField{
			{Tag: 1, Int: sint(1)}, {Tag: 2, Int: sint(2)},
			{Tag: 3, Msg: "aid"}, {Tag: 4, Msg: "deviceid"},
			{Tag: 5, Proc: "nonce"}, {Tag: 6, Str: str("!noperm!")},
			{Tag: 7, Int: sint(-888888)}, {Tag: 8, Int: sint(-888888)},
			{Tag: 9, Int: sint(3)}, {Tag: 10, Int: sint(-888888)},
			{Tag: 11, Str: str("!notset!")}, {Tag: 12, Str: str("Asia/Shanghai,8")},
			{Tag: 13, Str: str("zh_CN")}, {Tag: 14, Int: sint(4)},
			{Tag: 16, Fixed32: zero}, {Tag: 17, Fixed32: zero}, {Tag: 18, Fixed32: zero},
			{Tag: 19, Fixed32: zero}, {Tag: 20, Fixed32: zero}, {Tag: 21, Fixed32: zero},
			{Tag: 22, Query: "os_version"}, {Tag: 23, Int: sint(41)}, {Tag: 24, Int: sint(36)},
			{Tag: 25, Proc: "started"}, {Tag: 26, Proc: "started"}, {Tag: 27, Proc: "started"},
			{Tag: 28, Proc: "now"}, {Tag: 29, Int: sint(-1)},
			{Tag: 30, Query: "device_type"}, {Tag: 31, Str: str("HONOR")},
			{Tag: 32, Query: "device_type"}, {Tag: 33, Query: "device_type"},
			{Tag: 34, Str: str("HONOR")}, {Tag: 35, Str: str("HONOR")},
			{Tag: 36, Str: str("HONOR")}, {Tag: 38, Int: sint(31)},
		},
		Env: []EnvelopeField{
			{Tag: 1, Proc: "launch"}, {Tag: 2, Int: sint(247235599)},
			{Tag: 3, Int: sint(247235596)}, {Tag: 5, Int: sint(7)},
			{Tag: 6, Str: str("v04.10.03.04-bugfix")}, {Tag: 7, Proc: "pid"},
			{Tag: 8, Query: "channel"}, {Tag: 12, Msg: "device"},
			{Tag: 13, Nested: []EnvelopeField{
				{Tag: 1, Msg: "ts"}, {Tag: 2, Int: sint(-2)}, {Tag: 4, Int: sint(200)},
			}},
			{Tag: 14, Msg: "version"},
		},
		Nested15: []EnvelopeField{
			{Tag: 1, Int: sint(305)}, {Tag: 2, Int: sint(4)},
			{Tag: 3, Int: sint(694367)}, {Tag: 5, Int: sint(1621072674)},
		},
		Message: []EnvelopeField{
			{Tag: 16, Proc: "nonce"},
		},
	}
}
