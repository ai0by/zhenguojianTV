// Package hgv5 是红果 2026-11-19 后的 v5 取流协议实现。
//
// 上游在 2026-11 中旬把取流从 v4（播放器签名 X-Gorgon/Argus/Ladon、
// api5-normal-sinfonlineb、GET video_tab）整体升级到 v5：
//   - 新接口 POST https://api5-normal-sinfonlinea.fqnovel.com/novel/player/multi_video_model/player/v1
//   - 新签名 X-Khronos/X-Helios/X-Medusa（版本 7.4.3.32 / SDK v04.10.03）
//   - 请求体先 gzip 一次，同一份字节用于签名与发送；Content-Encoding: gzip
//   - 每台匿名设备只放行一次播放：空结果要换设备重签重发（上层负责）
//
// 本包是开源项目 github.com/beilunyang/openhongguo 的逐字节移植（纯标准库，
// 真网 live 契约 24/24 通过）。签名原语放在与本包同目录的 aes/f13/medusa 等
// 文件里；本文件只暴露 core 需要的高层构造 API。
//
// 刻意只做「注册 + 构造签名请求 + 解析响应体」——spade 地址解密与 CENC 密钥
// 派生与 core 既有实现逐字节一致，片源解析完全复用 core 的
// hongguoPlaybackModelFromResult，不在本包重复。
package hgv5

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 7.4.3.32 抓包常量（app 身份，不是设备凭据）。
const (
	Package        = "com.phoenix.read"
	V5VersionName  = "7.4.3.32"
	V5VersionCode  = "74332"
	RegisterVer    = "7.3.9.32" // 注册仍报 7.3.9.32 机型，见 registerHeader
	RegisterCode   = "73932"
	AID            = "8662"
	AppName        = "novelread"
	DevicePlatform = "android"

	RegisterHost = "log5-applog-lq.fqnovel.com"
	APIBase      = "https://api5-normal-sinfonlinea.fqnovel.com"
	PathPlayback = "/novel/player/multi_video_model/player/v1"
	PathRegister = "/service/2/device_register/"

	// device_level 必须保持 1：3 会让服务端把码流换成 bytevc2（无解码器）。
	deviceLevel = "1"
)

// Device 是 query / User-Agent 里出现的手机形态。留空字段用 DefaultDevice 补齐。
type Device struct {
	Brand, Model, OSVersion, Build, Resolution, Channel string
	OSAPI, DPI                                          int
}

// DefaultDevice 是一台 Xiaomi 14（与 openhongguo live 测试同一台，已验证可播）。
var DefaultDevice = Device{
	Brand: "Xiaomi", Model: "23127PN0CC", OSVersion: "14", OSAPI: 34,
	Build: "UKQ1.230804.001", Resolution: "1200*2670", DPI: 460, Channel: "xiaomi_8662_64",
}

func (d Device) orDefault() Device {
	def := DefaultDevice
	if d.Brand == "" {
		d.Brand = def.Brand
	}
	if d.Model == "" {
		d.Model = def.Model
	}
	if d.OSVersion == "" {
		d.OSVersion = def.OSVersion
	}
	if d.OSAPI == 0 {
		d.OSAPI = def.OSAPI
	}
	if d.Build == "" {
		d.Build = def.Build
	}
	if d.Resolution == "" {
		d.Resolution = def.Resolution
	}
	if d.DPI == 0 {
		d.DPI = def.DPI
	}
	if d.Channel == "" {
		d.Channel = def.Channel
	}
	return d
}

// PlaybackUserAgent 是 7.4.3.32 客户端发 v5 取流请求时带的 Cronet UA。
func PlaybackUserAgent() string {
	d := DefaultDevice
	return fmt.Sprintf("%s/%s (Linux; U; Android %s; zh_CN; %s; Build/%s; Cronet/TTNetVersion:8d40f833 2026-03-03 QuicVersion:21ac1950 2025-11-18)",
		Package, V5VersionCode, d.OSVersion, d.Model, d.Build)
}

func registerUserAgent() string {
	d := DefaultDevice
	return fmt.Sprintf("%s/%s (Linux; U; Android %s; zh_CN; %s; Build/%s; Cronet/TTNetVersion:8d40f833 2026-03-03 QuicVersion:21ac1950 2025-11-18)",
		Package, RegisterCode, d.OSVersion, d.Model, d.Build)
}

// Identity 是本地生成的匿名安装标识 + 注册后服务端下发的 device_id/install_id。
type Identity struct {
	CDID, OpenUDID, ClientUDID, OAID, ReqID string
	DeviceID, InstallID                     string
	Device                                  Device
}

// NewIdentity 用 crypto/rand 生成一组全新匿名身份。换设备重来时调它即可。
func NewIdentity() (Identity, error) {
	var raw [72]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return Identity{}, err
	}
	return Identity{
		CDID: uuid(raw[0:16]), OpenUDID: hex.EncodeToString(raw[16:24]),
		ClientUDID: uuid(raw[24:40]), OAID: uuid(raw[40:56]), ReqID: uuid(raw[56:72]),
		Device: DefaultDevice,
	}, nil
}

func uuid(b []byte) string {
	var id [16]byte
	copy(id[:], b)
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	var s [36]byte
	hex.Encode(s[0:8], id[0:4])
	s[8] = '-'
	hex.Encode(s[9:13], id[4:6])
	s[13] = '-'
	hex.Encode(s[14:18], id[6:8])
	s[18] = '-'
	hex.Encode(s[19:23], id[8:10])
	s[23] = '-'
	hex.Encode(s[24:36], id[10:16])
	return string(s[:])
}

// ---------- 注册（tt-data  envelopes） ----------

func registerHeader(id Identity, now time.Time) map[string]any {
	d := id.Device.orDefault()
	return map[string]any{
		"display_name": "红果免费短剧", "update_version_code": 73932, "manifest_version_code": 73932,
		"app_version_minor": RegisterVer, "aid": 8662, "channel": d.Channel, "package": Package,
		"app_version": RegisterVer, "version_code": 73932, "os": "Android", "os_version": d.OSVersion,
		"os_api": d.OSAPI, "device_model": d.Model, "device_brand": d.Brand,
		"device_manufacturer": d.Brand, "cpu_abi": "arm64-v8a", "device_platform": DevicePlatform,
		"language": "zh", "timezone": 8, "access": "wifi", "region": "CN",
		"tz_name": "Asia/Shanghai", "tz_offset": 28800, "cdid": id.CDID, "openudid": id.OpenUDID,
		"clientudid": id.ClientUDID, "ipv6_list": []any{}, "sim_serial_number": []any{},
		"oaid": buildBootstrapOAID(id.OAID, now.Add(-200*time.Millisecond)), "oaid_may_support": true,
		"req_id": id.ReqID, "guest_mode": 0, "is_system_app": 0, "sdk_flavor": "china",
		"device_id": "0", "install_id": "0",
	}
}

func buildBootstrapOAID(id string, issuedAt time.Time) map[string]any {
	return map[string]any{
		"req_id": id, "hw_id_version_code": "0", "take_ms": "6", "is_track_limited": "false",
		"query_times": "1", "id": id, "time": strconv.FormatInt(issuedAt.UnixMilli(), 10),
	}
}

var bootstrapOrder = []string{
	"display_name", "update_version_code", "manifest_version_code", "app_version_minor",
	"aid", "channel", "package", "app_version", "version_code", "os", "os_version", "os_api",
	"device_model", "device_brand", "device_manufacturer", "cpu_abi", "language", "timezone",
	"access", "region", "tz_name", "tz_offset", "cdid", "openudid", "clientudid", "ipv6_list",
	"sim_serial_number", "oaid", "oaid_may_support", "req_id", "device_platform", "guest_mode",
	"is_system_app", "sdk_flavor", "device_id", "install_id",
}

func marshalOrdered(values map[string]any) ([]byte, error) {
	keys := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, key := range bootstrapOrder {
		if _, ok := values[key]; ok {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	rest := make([]string, 0)
	for key := range values {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	keys = append(keys, rest...)
	var out bytes.Buffer
	out.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			out.WriteByte(',')
		}
		kb, _ := json.Marshal(key)
		out.Write(kb)
		out.WriteByte(':')
		vb, err := json.Marshal(values[key])
		if err != nil {
			return nil, err
		}
		out.Write(vb)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// BuildRegisterBody 生成注册请求体：magic_tag 信封 → gzip → tt-data 包装。
func BuildRegisterBody(id Identity, now time.Time) ([]byte, error) {
	header := registerHeader(id, now)
	headerJSON, err := marshalOrdered(header)
	if err != nil {
		return nil, err
	}
	var plain bytes.Buffer
	plain.WriteString(`{"magic_tag":"ss_app_log","header":`)
	plain.Write(headerJSON)
	plain.WriteString(`,"_gen_time":`)
	genTime, _ := json.Marshal(now.UnixMilli())
	plain.Write(genTime)
	plain.WriteByte('}')
	var gz bytes.Buffer
	w, err := gzip.NewWriterLevel(&gz, gzip.DefaultCompression)
	if err != nil {
		return nil, err
	}
	w.Header.ModTime = now.UTC()
	w.Header.OS = 255
	if _, err = w.Write(plain.Bytes()); err != nil {
		return nil, err
	}
	if err = w.Close(); err != nil {
		return nil, err
	}
	return TTEncrypt(gz.Bytes()), nil
}

var (
	ttEncryptConstant, _ = hex.DecodeString("4dd4c2e6b83162090e52b3c7a6733ba41cb2462b829ab58a196b39db57177524f49baf7f08e8d68d26a72e37c1a95a2f1f05a51892aef2949732b62a38aadd58")
	ttEncryptPrefix, _   = hex.DecodeString("3176c9b6d71579d7e82179c9e7cbde787b54d541bad3b15f2e8fed92293fa25a")
)

// TTEncrypt 把字节包进 74 63 05 10 00 00 开头的 tt-data 信封。
func TTEncrypt(input []byte) []byte {
	prefixHash := sha512.Sum512(ttEncryptPrefix)
	keyIV := sha512.Sum512(append(prefixHash[:], ttEncryptConstant...))
	digest := sha512.Sum512(input)
	plain := make([]byte, 0, len(digest)+len(input)+16)
	plain = append(plain, digest[:]...)
	plain = append(plain, input...)
	padding := 16 - len(plain)%16
	for i := 0; i < padding; i++ {
		plain = append(plain, byte(padding))
	}
	block, err := aes.NewCipher(keyIV[:16])
	if err != nil {
		return nil
	}
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, keyIV[16:32]).CryptBlocks(out, plain)
	result := make([]byte, 0, 6+len(ttEncryptPrefix)+len(out))
	result = append(result, 0x74, 0x63, 0x05, 0x10, 0x00, 0x00)
	result = append(result, ttEncryptPrefix...)
	result = append(result, out...)
	return result
}

// RegisterHeaders 返回注册请求的 query 参数与 header。
func RegisterHeaders(id Identity, body []byte) (map[string]string, map[string]string) {
	query := map[string]string{"req_id": id.ReqID, "tt_data": "a"}
	sum := md5Raw(body)
	headers := map[string]string{
		"User-Agent":                registerUserAgent(),
		"Content-Type":              "application/octet-stream;tt-data=a",
		"log-encode-type":           "gzip",
		"sdk-version":               "2",
		"passport-sdk-version":      "5051452",
		"x-vc-bdturing-sdk-version": "4.0.3.cn",
		"x-ss-dp":                   AID,
		"X-SS-STUB":                 strings.ToUpper(hex.EncodeToString(sum[:])),
	}
	return query, headers
}

// ParseRegister 解析注册响应；device_id=0 视为失败。
func ParseRegister(raw []byte) (deviceID, installID string, err error) {
	var env struct {
		DeviceID  flexString `json:"device_id_str"`
		InstallID flexString `json:"install_id_str"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", "", err
	}
	deviceID, installID = env.DeviceID.String(), env.InstallID.String()
	if deviceID == "" || deviceID == "0" || installID == "" || installID == "0" {
		return "", "", errors.New("红果注册未返回有效设备号")
	}
	return deviceID, installID, nil
}

type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = flexString(n.String())
	return nil
}
func (f flexString) String() string { return string(f) }

// ---------- 取流 query / body / 签名 ----------

// PlaybackPairs 按 7.4.3.32 抓包顺序生成 30 个 query 参数（顺序是签名的一部分）。
// _rticket 留在第 4 位由 signer 就地填写。
func PlaybackPairs(id Identity) [][2]string {
	d := id.Device.orDefault()
	version := V5VersionCode
	return [][2]string{
		{"device_platform", DevicePlatform}, {"os", "android"}, {"ssmix", "a"}, {"_rticket", ""},
		{"cdid", id.CDID}, {"channel", d.Channel}, {"aid", AID}, {"app_name", AppName},
		{"version_code", version}, {"version_name", V5VersionName},
		{"manifest_version_code", version}, {"update_version_code", version},
		{"resolution", d.Resolution}, {"dpi", strconv.Itoa(d.DPI)}, {"device_type", d.Model},
		{"device_brand", d.Brand}, {"language", "zh"}, {"os_api", strconv.Itoa(d.OSAPI)},
		{"os_version", d.OSVersion}, {"ac", "wifi"}, {"host_abi", "arm64-v8a"},
		{"dragon_device_type", "phone"}, {"pv_player", version}, {"compliance_status", "0"},
		{"need_personal_recommend", "1"}, {"player_so_load", "1"}, {"is_android_pad_screen", "0"},
		{"iid", id.InstallID}, {"device_id", id.DeviceID},
	}
}

// BuildPlaybackBody 生成 gzip 后的请求体；同一份字节既用于签名也用于发送。
func BuildPlaybackBody(episodeID string) ([]byte, error) {
	body, err := json.Marshal(map[string]any{
		"biz_param": map[string]any{
			"detail_page_version": 0, "device_level": 1, "disable_digg_stat": false,
			"disable_video_relate_book": false, "need_all_video_definition": true,
			"need_mp4_align": false, "use_os_player": false, "use_server_dns": false,
			"video_platform": 1024,
		},
		"mixed_video_id_map": map[string][]string{"1004": {episodeID}},
		"dr_scene":           "player",
	})
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	if _, err = w.Write(body); err != nil {
		return nil, err
	}
	if err = w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// SignRequest 对 V5 取流请求签名，返回最终 query 串与需要设置的 header。
func SignRequest(id Identity, pairs [][2]string, body []byte, nowMillis int64) (string, map[string]string, error) {
	signer := NewPlaybackV5Signer(nowMillis)
	query, headers, err := signer.Sign(pairs, nowMillis, body, nil)
	if err != nil {
		return "", nil, err
	}
	return query, headers, nil
}
