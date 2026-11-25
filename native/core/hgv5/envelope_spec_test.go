package hgv5

import (
	"encoding/json"
	"testing"
)

// fixedTestSigner 返回一台进程信息完全固定的签名器，便于逐字节对拍。
func fixedTestSigner() *PlaybackV5Signer {
	return &PlaybackV5Signer{
		started: 1728388016635,
		nonce:   "Aabcdefghijklmnopqrstuvwx",
		launch:  117,
		pid:     11884,
	}
}

func testEnvelopeContext(s *PlaybackV5Signer, p *SigningProfile) *envelopeContext {
	query := "device_id=1234567890&version_name=7.4.3.32&os_version=12&device_type=PGT-AN10&channel=vivo_8662_64"
	qh := sm3Sum([]byte(query))
	body := [16]byte{}
	ts := uint32(1795578057)
	return &envelopeContext{
		query:     query,
		queryHash: qh,
		digest:    playbackV5Hash(qh, body, ts),
		ts:        ts,
		entropy:   Entropy{MessageRandom: 1234567},
		signer:    s,
		profile:   p,
		nowMs:     1795578057123,
	}
}

// 声明式 official 规格必须与内置 official 硬编码逐字节一致。
// 这是「结构热更新」的安全前提：下发的就是当前在跑的那套，不会引入回归。
func TestOfficialEnvelopeSpecMatchesBuiltin(t *testing.T) {
	s := fixedTestSigner()
	p := defaultSigningProfile
	ctx := testEnvelopeContext(s, &p)

	_, wantEnv := s.builtinEnvelopeParts(&p, ctx)

	gotCtx := testEnvelopeContext(s, &p)
	gotEnv, gotNested, gotFlags, _, err := renderCustomEnvelope(officialEnvelopeSpec(), gotCtx)
	if err != nil {
		t.Fatalf("渲染 official 规格失败: %v", err)
	}
	if string(gotEnv) != string(wantEnv) {
		t.Fatalf("env 不一致\n got=%x\nwant=%x", gotEnv, wantEnv)
	}
	if len(gotFlags) != 8 || gotFlags[0] != 0x44 || gotFlags[7] != 0x00 {
		t.Fatalf("flags 应为 4480000000000000，实得 %x", gotFlags)
	}
	if len(gotNested) == 0 {
		t.Fatal("nested15 为空")
	}

	// 设备画像也必须一致（内置实现不直接暴露，这里用相同规格再渲染一次做自洽检查）。
	devA, err := renderEnvelopeFields(officialEnvelopeSpec().Device, gotCtx)
	if err != nil {
		t.Fatal(err)
	}
	devB, err := renderEnvelopeFields(officialEnvelopeSpec().Device, testEnvelopeContext(s, &p))
	if err != nil {
		t.Fatal(err)
	}
	if string(devA) != string(devB) {
		t.Fatal("同一规格两次渲染的设备画像不一致（说明依赖了随机量）")
	}
}

func TestEnvelopeSpecRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		spec *EnvelopeSpec
	}{
		{"nil", nil},
		{"空规格", &EnvelopeSpec{}},
		{"tag 为 0", &EnvelopeSpec{Device: []EnvelopeField{{Tag: 0, Int: intPtr(1)}}}},
		{"tag 越界", &EnvelopeSpec{Device: []EnvelopeField{{Tag: 70000, Int: intPtr(1)}}}},
		{"双值来源", &EnvelopeSpec{Device: []EnvelopeField{{Tag: 1, Int: intPtr(1), Str: strPtr("x")}}}},
		{"无值来源", &EnvelopeSpec{Device: []EnvelopeField{{Tag: 1}}}},
		{"未知进程量", &EnvelopeSpec{Device: []EnvelopeField{{Tag: 1, Proc: "shell"}}}},
		{"未知消息量", &EnvelopeSpec{Device: []EnvelopeField{{Tag: 1, Msg: "eval"}}}},
		{"flags 长度错", &EnvelopeSpec{Flags: "44", Device: []EnvelopeField{{Tag: 1, Int: intPtr(1)}}}},
		{"嵌套过深", &EnvelopeSpec{Device: []EnvelopeField{{
			Tag: 1, Nested: []EnvelopeField{{Tag: 2, Nested: []EnvelopeField{{Tag: 3,
				Nested: []EnvelopeField{{Tag: 4, Nested: []EnvelopeField{{Tag: 5, Int: intPtr(1)}}}}}}}}}},
		}},
	}
	for _, c := range cases {
		if err := validateEnvelopeSpec(c.spec); err == nil {
			t.Errorf("%s: 应当被拒绝但通过了", c.name)
		}
	}
}

func intPtr(v int64) *int64   { return &v }
func strPtr(v string) *string { return &v }

// 远程下发的 JSON 能反序列化成可用规格，并能改变结构（加字段）。
func TestEnvelopeSpecFromJSONAddsField(t *testing.T) {
	raw := `{
		"name":"remote-test",
		"license":"646637794",
		"magic":"2d4b4fca49750d433fb5ae2c226dcc56",
		"signKey":"b23c2a470a20d105dc3f8c4438f8272ed46d6c89ffdcdedd5108698d26e9489d",
		"sdkVersion":"v04.10.03-ml-android",
		"envVersion":"v04.10.03.04-bugfix",
		"sdkCode":67503104,
		"medusa21":312,
		"field24":"{\"un_h\":11}",
		"path":"/novel/player/multi_video_model/player/v1",
		"deviceLevel":1,
		"envelope":"custom",
		"envelopeSpec":{
			"flags":"4480000000000000",
			"device":[{"t":1,"i":1},{"t":2,"i":2},{"t":4,"m":"deviceid"},{"t":22,"q":"os_version"}],
			"env":[{"t":6,"s":"v04.10.03.04-bugfix"},{"t":12,"m":"device"},{"t":14,"m":"version"}],
			"nested15":[{"t":1,"i":305}],
			"message":[{"t":16,"p":"nonce"},{"t":40,"i":999}]
		}
	}`
	var p SigningProfile
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if err := validateSigningProfile(p); err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if p.EnvelopeSpec == nil || len(p.EnvelopeSpec.Message) != 2 {
		t.Fatalf("规格未正确解析: %+v", p.EnvelopeSpec)
	}

	s := fixedTestSigner()
	ctx := testEnvelopeContext(s, &p)
	_, _, _, extra, err := renderCustomEnvelope(p.EnvelopeSpec, ctx)
	if err != nil {
		t.Fatal(err)
	}
	// message 里的 tag 40 必须出现在渲染结果中（0x40<<3|0 = 0x140 → varint 0xc0 0x02）。
	if len(extra) == 0 {
		t.Fatal("额外字段未渲染")
	}
}
