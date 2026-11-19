package core

// 临时探针：移植 guoapp 红果模块 v1.0.4 的 playback v5 签名，验证真实上游取流。
// 验证完立即删除，不入库。

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"math/bits"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

var v5Magic, _ = hex.DecodeString("2d4b4fca49750d433fb5ae2c226dcc56")
var v5SignKey, _ = hex.DecodeString("b23c2a470a20d105dc3f8c4438f8272ed46d6c89ffdcdedd5108698d26e9489d")

const v5SboxHex = "53290fe9e51f316f90f74a7e034d367784ab49237cd0135cdc632c322fd3be0e2624a417ecd9151a7ff64f60730a87f9445ec6ada2f865d22b696c4188f24c0d" +
	"f37b75510b939f6bc2fe6883e68bc8c722181ba0bdb08c9871b50c9a16b46eea8eed20ba4b8a14e0fcf1468dbbef05a93e5a9d7dcf06898635c1fd99f5de287a" +
	"80c594daaab19b6279fbd75070cb52eb2555c456e39e7839853874e2f0013ac0b64742d8ca10e4b79c955de8b843ce4ed464a6a72ed508091c92e7b3eedd1e1d" +
	"d12afac321618fa3d65f9167c982f43d66a53c6a1997585b346dbf9672ff37cdbc073b2dae11453304df00815476b2a13fccdb30b957a8acafe1274002485912" +
	"38922563e36406d37a24100b7902a028044e218453c2a399199c9d13dc40c55ba67e502273850ebdc1a2e2188b8f2d9e3cb909b8c46a2a9f4a6dd93007eafa65" +
	"b7b3a881aa908cc74f0ad76bbbb58617c37012ca05a469fd44e62eecf1e4272ce860c0ae5eadf99b20d8f0234174006f3f110d7657d01645d4eff8f71471cbfe" +
	"1cb2f52648727bf3c9de55153e031b4d61757db63dbf7782eeafe9018a8891362fb14759ac32bab03b349aeb5a377c5156be4c8dfbe13a684bda39cf836697c8" +
	"94545fe0f2491acea9fc6252f4ff1e0fe72bd2df6e67a7f635e5bcc6d1a5dd951d89960887a18eb40c785cdb1f80ed42299333ab98cdd67f316cd54346585dcc" +
	"d759fb722789b08807cc0fda293f7e5e1819bb0353517020b31d3d00374952bea2c421e862040a9ec892a05b266b7a0d0cc316f483b81af39715657ffe32ad8d" +
	"e0c13077646035236ee5363e5f900e9a7cf850a81cd9960b6df73317226f99eaa6fff15cec34acb98246639b918ab2851466a7cdfc4ad1b52c2ae95a58d3734e" +
	"47ddd5b7560193f644ef9c408b7dee39b4d6aabf4de761e3252d4c7ba3843187745d55e6a5db05d42b45caed43c0c5d0b64bfa9d94a4a9d8f2f0e1a108dcfd28" +
	"8fd2bc1fc7bd675412138169cb09249875ba3b68cfb11180afae104fc9789571c6dfce02413886796ae21e3c488cc21b9f423a066c2eabf98e2fdeeb5776e4f5" +
	"d09a515ba58cbbab3e37b09fd4f6d9244df80512e84cd7956be301cf64f1429d58577cc241f3a2c655144f81aaf95a4e108da894b38970561ae9002619fbbac9" +
	"1fa922ed202bfc62cb08e0de2a7f3631435f8b47c8a3c563b4db4071ce74c46da6fa696a9e282d1e825d663b93bfd56c48dce1884bb10fccea063929f746443f" +
	"d830a4232e53da21a1f2d1bdfebc3a02c10a1b8ad31d04fd616590d272ac737e98eb33af60528716bee78ec0ee8699031177277d9cff32542c67caf5f4459168" +
	"1335ae0e178f3409a7803db83892dde5b9b7f0c750e24aef077badd678a09b850b84ec1597e4963cb5e675df1c0d6fb618496ecd76c35c798325590c2fb27a5e"

var v5Sbox, _ = hex.DecodeString(v5SboxHex)

var v5HashTable = []uint32{
	0xebb64faf, 0x07aadcc2, 0xcf3187bf, 0xe01138ff, 0x6d0bfcff, 0x5a30a3be, 0xb41ad638, 0x34180eb8,
	0xf233eb6f, 0xb1a584cc, 0xccc30dc7, 0x47d1db51, 0xd55653de, 0x70a84fa1, 0x57473c12, 0xf76f0288,
	0x2c077f0a, 0xda0dcad0, 0xfbb86f6c, 0xfdc4cf00, 0x688a020d, 0xe676c6a6, 0x8cd6338b, 0x1a3c8d0e,
	0xcce8b06b, 0x6ad0ed0b, 0xa0522717, 0xdc71ac83, 0x2285db71, 0xd5b4dda6, 0x736f8650, 0x6560306c,
	0x617ce2a6, 0xe423417e, 0x0a40e143, 0x544e4032, 0x88dffb2a, 0x716c1ae0, 0x4c467a88, 0x05b23bb3,
	0xe1d0b866, 0xbaa3dcb8, 0xae3374d3, 0xc3381a50, 0x1702f75b, 0xfe6da368, 0xf0b4cf48, 0x4e0ffbb8,
	0x72aad10d, 0x26c53a3d, 0xf2bce0f6, 0xb4557581, 0x4a257fdd, 0x8c3182a2, 0xab0b3b86, 0x3d5dfb14,
	0x4f103634, 0xd37b52d7, 0x444eff16, 0xeb0a33d1, 0x6ca86f6e, 0x00284ba7, 0x08387cfa, 0x5fb37586,
}

func v5le32(v uint32) []byte { return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)} }
func v5u32le(b []byte, o int) uint32 {
	return uint32(b[o]) | uint32(b[o+1])<<8 | uint32(b[o+2])<<16 | uint32(b[o+3])<<24
}
func v5rotl8(v byte, n uint) byte { return bits.RotateLeft8(v, int(n)) }

func v5IV(queryHash, bodyMd5 []byte, ts uint32) uint32 {
	value := uint32(0x20230928)
	for _, data := range [][]byte{queryHash, bodyMd5, v5le32(ts)} {
		for i, b := range data {
			if i&1 == 0 {
				value = (value >> 4) ^ value ^ (value << 6) ^ uint32(b)
			} else {
				value = ^((value >> 7) ^ value ^ (uint32(b) | (value << 12)))
			}
		}
	}
	return value
}

func putU32BE(b []byte, o int, v uint32) {
	b[o], b[o+1], b[o+2], b[o+3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
}

func v5Hash(queryHash, bodyMd5 []byte, ts uint32) []byte {
	if len(queryHash) != 32 || len(bodyMd5) != 16 {
		return nil
	}
	if v5IV(queryHash, bodyMd5, ts)&15 != 12 {
		return nil
	}
	iv := uint32(0x9d4a97c2)
	count := iv + ts
	table := make([]uint32, len(v5HashTable))
	for i, v := range v5HashTable {
		table[i] = bits.RotateLeft32(v, -int((uint32(count)+1)&255))
	}
	shift := uint((uint32(count) + 2) & 7)
	padding := []byte{0xfa, 0x45, 0x61, 0xd7}
	for i, v := range padding {
		padding[i] = byte((uint32(v) | uint32(v)<<8) >> shift)
	}
	data := append([]byte(nil), queryHash...)
	data = append(data, bodyMd5...)
	data = append(data, v5le32(ts)...)
	data = append(data, padding...)
	data = append(data, 0, 0, 0, 0, 0, 0, 1, 0xa0)
	words := make([]uint32, 0, 128)
	for i := 0; i < len(data); i += 4 {
		words = append(words, binary.BigEndian.Uint32(data[i:]))
	}
	for i := 0; i < 112; i++ {
		x, y := words[i+1], words[i+14]
		words = append(words, words[i]+words[i+9]+(bits.RotateLeft32(x, 14)^bits.RotateLeft32(x, 25)^(x>>3))+
			(bits.RotateLeft32(y, 13)^bits.RotateLeft32(y, 15)^(y>>10)))
	}
	initial := []uint32{0x7aba4fc8, 0x67166507, 0x6403fa00, 0x340f512f, 984304912, 3005047866, 2874125293, 2152413264}
	for i := range initial {
		initial[i] = bits.RotateLeft32(initial[i], -int(uint32(count)))
	}
	state := append([]uint32(nil), initial...)
	for i := 0; i < 96; i++ {
		base := uint32(iv + uint32(i))
		choose := ((state[4] ^ state[0]) & state[1]) ^ state[4]
		sigma1 := bits.RotateLeft32(state[1], 26) ^ bits.RotateLeft32(state[1], 21) ^ bits.RotateLeft32(state[1], 7)
		mixed := words[base&127] + choose + sigma1 + table[base&63] + state[6]
		sigma0 := bits.RotateLeft32(state[3], 30) ^ bits.RotateLeft32(state[3], 19) ^ bits.RotateLeft32(state[3], 10)
		majority := (state[3] & state[5]) | ((state[3] | state[5]) & state[2])
		old := state[7]
		state = append(state[1:], state[0])
		state[7] = sigma0 + majority + mixed
		state[0] = old + mixed
	}
	digest := make([]byte, 32)
	for i := 0; i < 8; i++ {
		putU32BE(digest, i*4, state[i]+initial[i])
	}
	folded := make([]byte, 16)
	for i := 0; i < 16; i++ {
		folded[i] = digest[i] ^ digest[i+16]
	}
	check := uint32(0x20220420)
	for i := 0; i < 12; i++ {
		var temp uint32
		if i&1 == 0 {
			temp = (check >> 3) ^ check
			check = uint32(folded[i]) ^ (check << 7) ^ temp
		} else {
			temp = (check >> 5) ^ check
			check = ^(uint32(folded[i]) | (check << 11)) ^ temp
		}
	}
	return append(folded, v5le32(((check|4)^0x1000000)|0x20)...)
}

func v5Carrier(keyBytes, ivBytes, source []byte, ts uint32) []byte {
	variant := ts & 3
	box := v5Sbox[variant*256 : (variant+1)*256]
	con := [][]int{{2, 0, 3, 1}, {1, 3, 2, 0}, {3, 2, 1, 0}, {2, 3, 1, 0}}[variant]
	order := [][]int{
		{0, 9, 14, 15, 4, 13, 2, 7, 8, 1, 6, 3, 12, 5, 10, 11},
		{0, 9, 14, 11, 4, 13, 2, 3, 8, 1, 6, 15, 12, 5, 10, 7},
		{0, 9, 14, 3, 4, 13, 2, 15, 8, 1, 6, 11, 12, 5, 10, 7},
		{0, 9, 14, 15, 4, 13, 2, 3, 8, 1, 6, 11, 12, 5, 10, 7},
	}[variant]
	initial := []uint32{0xbbb5de72, 0xd4638d5b, 0x92a5f73b, 0x89f8a1c9}[variant]
	initialBytes := v5le32(initial)
	expanded := make([]byte, 48)
	for i := 0; i < 16; i++ {
		expanded[i] = initialBytes[i&3] ^ keyBytes[i]
	}
	rounds := uint(8)
	for i := 4; i < 12; i++ {
		at := 4 * (i - 1)
		a, b, c, d := expanded[at], expanded[at+1], expanded[at+2], expanded[at+3]
		if i&3 == 0 {
			next := byte((uint32(initial) >> (rounds & 24)) ^ uint32(box[b]))
			b, c, d, a = box[c], box[d], box[a], next
		}
		rounds += 2
		expanded[at+4], expanded[at+5], expanded[at+6], expanded[at+7] = a^expanded[at-12], b^expanded[at-11], c^expanded[at-10], d^expanded[at-9]
	}
	xt := func(v byte) byte { return (v << 1) ^ ((v & 128) / 128 * 0x1b) }
	blockFn := func(value []byte) []byte {
		rows := make([][]byte, 4)
		for i := range rows {
			rows[i] = append([]byte(nil), value[i*4:i*4+4]...)
		}
		conAdd := func(off int) {
			for i := 0; i < 4; i++ {
				for j := 0; j < 4; j++ {
					rows[i][j] ^= expanded[(off+i)*4+con[j]]
				}
			}
		}
		conAdd(0)
		for r := 1; r <= 2; r++ {
			for i := 0; i < 4; i++ {
				for j := 0; j < 4; j++ {
					rows[i][j] = box[rows[i][j]]
				}
			}
			before := make([][]byte, 4)
			for i := range before {
				before[i] = append([]byte(nil), rows[i]...)
			}
			for i := range rows {
				rows[i] = before[con[i]]
			}
			flat := make([]byte, 0, 16)
			for _, row := range rows {
				flat = append(flat, row...)
			}
			for i := 0; i < 16; i++ {
				rows[i/4][i%4] = flat[order[i]]
			}
			if r == 1 {
				tmp := make([][]byte, 4)
				for i := range tmp {
					tmp[i] = make([]byte, 4)
					for j := 0; j < 4; j++ {
						tmp[i][j] = rows[i][con[j]]
					}
				}
				rows = tmp
				for i := 0; i < 4; i++ {
					mixed := rows[0][i] ^ rows[1][i] ^ rows[2][i] ^ rows[3][i]
					first := rows[0][i]
					rows[0][i] ^= mixed ^ xt(rows[0][i]^rows[1][i])
					rows[1][i] ^= mixed ^ xt(rows[1][i]^rows[2][i])
					rows[2][i] ^= mixed ^ xt(rows[2][i]^rows[3][i])
					rows[3][i] ^= mixed ^ xt(rows[3][i]^first)
				}
			}
			conAdd(r * 4)
		}
		for i := 0; i < 4; i++ {
			for j := 0; j < 4; j++ {
				rows[i][j] ^= expanded[(4+i)*4+j]
			}
		}
		out := make([]byte, 0, 16)
		for _, row := range rows {
			out = append(out, row...)
		}
		return out
	}
	wire := []uint{6, 5, 3, 4, 2, 7, 1, 0}
	compact := []uint{1, 7, 3, 6, 2, 0, 4, 5}
	plaintext := make([]byte, 32)
	for i := 0; i < 31; i++ {
		for j := 0; j < 8; j++ {
			plaintext[i] |= ((source[i*8+j] >> wire[j]) & 1) << compact[j]
		}
	}
	plaintext[31] = 1
	input := make([]byte, 16)
	for i := 0; i < 16; i++ {
		input[i] = plaintext[i] ^ ivBytes[i]
	}
	first := blockFn(input)
	for i := 0; i < 16; i++ {
		input[i] = plaintext[i+16] ^ first[i]
	}
	key := append(append([]byte(nil), first...), blockFn(input)...)
	out := append([]byte{key[31]}, source...)
	for i := 0; i < 31; i++ {
		for j := 0; j < 8; j++ {
			at := 1 + i*8 + j
			out[at] = (out[at] &^ (1 << wire[j])) | (((key[i] >> compact[j]) & 1) << wire[j])
		}
	}
	return out
}

func varintField(out []byte, field uint64, value uint64) []byte {
	return binary.AppendUvarint(binary.AppendUvarint(out, field*8), value)
}
func bytesField(out []byte, field uint64, value []byte) []byte {
	out = binary.AppendUvarint(binary.AppendUvarint(out, field*8+2), uint64(len(value)))
	return append(out, value...)
}

func v5Proto(fields [][2]any) []byte {
	var out []byte
	for _, f := range fields {
		tag := f[0].(int)
		switch v := f[1].(type) {
		case int:
			val := uint64(v)
			if v < 0 {
				val = uint64(-v*2 - 1)
			} else {
				val = uint64(v * 2)
			}
			out = binary.AppendUvarint(binary.AppendUvarint(out, uint64(tag)*8), val)
		case uint64:
			out = binary.AppendUvarint(binary.AppendUvarint(out, uint64(tag)*8), v*2)
		case string:
			if v != "" {
				out = bytesField(out, uint64(tag), []byte(v))
			}
		case []byte:
			if len(v) != 0 {
				out = bytesField(out, uint64(tag), v)
			}
		}
	}
	return out
}

func v5Xmxor(data, key []byte) []byte {
	encoded := make([]byte, len(data))
	for i := 0; i < len(data); i++ {
		at := (i * 4) & 28
		d0, d1 := key[at], key[at+1]
		mixed := ^byte(v5rotl8(data[i], 4)+d0) ^ d1
		mixed = v5rotl8(mixed, 3)
		mixed = byte(mixed + d1)
		mixed ^= d0
		encoded[len(data)-i-1] = ^mixed
	}
	last := len(encoded) - 1
	lastV := encoded[last] ^ encoded[last-1]
	first := encoded[0]
	encoded[0] = byte((^lastV + first) & 255)
	encoded[1] = byte((int(encoded[0]^encoded[last]^254) + int(encoded[1])) & 255)
	encoded[2] = byte((int(encoded[2]) + int(byte((lastV-first)^v5rotl8(encoded[1], 3)^2))) & 255)
	for i := 0; i < len(encoded)-4; i++ {
		mixed := v5rotl8(encoded[i+2], 3) ^ encoded[i+1] ^ byte(i+3)
		encoded[i+3] = byte((int(^mixed) + int(encoded[i+3])) & 255)
	}
	encoded[last] ^= encoded[last-1]
	var sum uint32
	for i := 1; i < len(encoded); i++ {
		sum += uint32(encoded[i])
	}
	encoded[0] = byte((int(encoded[0]^encoded[1]) + int(sum)) & 255)
	return encoded
}

func v5readU64(b []byte, o int) uint64 {
	return uint64(v5u32le(b, o)) | uint64(v5u32le(b, o+4))<<32
}
func v5rotl64(v uint64, n int) uint64 { return bits.RotateLeft64(v, n) }
func v5writeU64(b []byte, o int, v uint64) {
	binary.LittleEndian.PutUint64(b[o:], v)
}

func v5Helios(ts uint32, prefix []byte) string {
	ascii := []byte(hex.EncodeToString(func() []byte {
		h := md5.Sum(append(append([]byte(nil), prefix...), []byte("8662")...))
		return h[:]
	}()))
	left, right := v5readU64(ascii, 0), v5readU64(ascii, 8)
	queue := []uint64{v5readU64(ascii, 16), v5readU64(ascii, 24)}
	var keys []uint64
	for i := 0; i < 34; i++ {
		keys = append(keys, left)
		mixed := (v5rotl64(right, -8) + left) ^ uint64(i)
		queue = append(queue, mixed)
		left = mixed ^ v5rotl64(left, 3)
		right, queue = queue[0], queue[1:]
	}
	data := []byte(strconv.FormatUint(uint64(ts), 10) + "-646637794-8662")
	padding := 16 - len(data)%16
	for len(data)%16 != 0 {
		data = append(data, byte(padding))
	}
	for off := 0; off < len(data); off += 16 {
		a, b := v5readU64(data, off), v5readU64(data, off+8)
		for _, key := range keys {
			b = key ^ (a + v5rotl64(b, -8))
			a = b ^ v5rotl64(a, 3)
		}
		v5writeU64(data, off, a)
		v5writeU64(data, off+8, b)
	}
	return base64.StdEncoding.EncodeToString(append(append([]byte(nil), prefix...), data...))
}

type v5Who struct{ deviceID, versionName, osVersion, deviceType, channel, deviceBrand string }

var v5ProcNonce []byte
var v5ProcStarted int64

func v5Medusa(queryHash, f13 []byte, ts uint32, who v5Who, nowMs int64) string {
	if v5ProcNonce == nil {
		raw := make([]byte, 18)
		for i := range raw {
			raw[i] = byte(time.Now().UnixNano() >> uint(i*7))
		}
		v5ProcNonce = []byte(base64.RawURLEncoding.EncodeToString(raw))
		v5ProcStarted = nowMs
	}
	flags := make([]byte, 8)
	deviceInfo := v5Proto([][2]any{
		{1, 1}, {2, 2}, {3, "8662"}, {4, who.deviceID}, {5, string(v5ProcNonce)},
		{6, "!noperm!"}, {12, "Asia/Shanghai,8"}, {13, "zh_CN"}, {22, who.osVersion},
		{25, v5ProcStarted}, {30, who.deviceType}, {31, who.deviceBrand},
		{32, who.deviceType}, {34, who.deviceBrand},
	})
	env := v5Proto([][2]any{{6, "v04.10.03.04-bugfix"}, {8, who.channel}, {12, deviceInfo}, {14, who.versionName}})
	msgRand4 := uint64(binary.LittleEndian.Uint32([]byte{
		byte(v5ProcStarted >> 7), byte(v5ProcStarted >> 14), byte(v5ProcStarted >> 21), byte(v5ProcStarted >> 28),
	}))
	message := v5Proto([][2]any{
		{1, v5Magic}, {2, 5}, {3, msgRand4}, {4, "8662"},
		{5, who.deviceID}, {6, "646637794"}, {7, who.versionName}, {8, "v04.10.03-ml-android"},
		{9, uint64(0x040a0300)}, {10, flags}, {12, uint64(ts)}, {13, f13}, {14, queryHash[:6]},
		{15, v5Proto([][2]any{{1, 1}, {2, 1}, {3, 1}, {5, 1}})}, {17, uint64(ts)}, {20, "none"},
		{21, 377}, {23, env},
		{24, `{"cmr":16777216,"cmr2":16777216,"un_h":11,"vpn":0,"kd":0,"dyn":"","do":1,"tk":true}`},
	})
	nonce := uint32(time.Now().UnixNano() & 0xffffffff)
	sm := hongguoSM3(append(append(append([]byte(nil), v5SignKey...), v5le32(nonce)...), v5SignKey...))
	high := byte((nonce >> 16) & 255)
	seed := v5le32(^(((uint32(high) << 11) | (nonce >> 24)) ^ (uint32(high) >> 5) ^ uint32(high)))
	transformed := append(append([]byte(nil), flags...), v5Xmxor(message, sm[:])...)
	for i, j := 0, len(transformed)-1; i < j; i, j = i+1, j-1 {
		transformed[i], transformed[j] = transformed[j], transformed[i]
	}
	for i := range transformed {
		transformed[i] ^= seed[(^i)&3]
	}
	check := uint32(0x18000001) | (uint32(queryHash[0]&63) << 14) | (uint32(f13[0]&63) << 8)
	packed := append([]byte{0xca}, make([]byte, 4)...)
	packed = append(packed, v5le32(check)...)
	packed = append(packed, transformed...)
	packed = append(packed, byte((nonce>>16)&255), byte((nonce>>24)&255))
	aesKey := md5.Sum(v5SignKey[:16])
	iv := md5.Sum(v5SignKey[16:])
	encrypted := v5Carrier(aesKey[:], iv[:], packed, ts)
	prefix := append(append([]byte(nil), v5le32(5)...), v5Magic...)
	stamp := v5le32(ts)
	for i := range prefix {
		prefix[i] ^= stamp[i&3]
	}
	result := append(prefix, byte(nonce&255), byte((nonce>>8)&255), 0, 1)
	result = append(result, encrypted...)
	return base64.StdEncoding.EncodeToString(result)
}

func v5QueryEscape(s string) string {
	return url.QueryEscape(s)
}

func v5EncodeQuery(values url.Values) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		vs := values[k]
		if len(vs) == 0 {
			continue
		}
		parts = append(parts, v5QueryEscape(k)+"="+v5QueryEscape(vs[0]))
	}
	return strings.Join(parts, "&")
}

func v5QueryValue(query, name string) string {
	parsed, _ := url.ParseQuery(query)
	return parsed.Get(name)
}

func TestHongguoV5PlaybackProbe(t *testing.T) {
	if os.Getenv("HG_V5_PROBE") != "1" {
		t.Skip("临时 v5 探针")
	}
	vid := os.Getenv("HG_V5_VID")
	if vid == "" {
		vid = "7708703745550339097"
	}
	d := probeDownloader(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := d.ensureHongguoDevice(ctx); err != nil {
		t.Fatalf("设备注册失败: %v", err)
	}
	identity := d.hongguoFallbackPlaybackIdentity()
	t.Logf("deviceID=%s installID=%s", identity.DeviceID, identity.InstallID)

	base := "https://api5-normal-sinfonlinea.fqnovel.com"
	path := "/novel/player/multi_video_model/player/v1"
	ua := "com.phoenix.read/73932 (Linux; U; Android 12; zh_CN_#Hans; PGT-AN10; Build/V417IR;tt-ok/3.12.13.20)"
	q := url.Values{}
	q.Set("aid", "8662")
	q.Set("app_name", "novelread")
	q.Set("version_code", "74332")
	q.Set("version_name", "7.4.3.32")
	q.Set("channel", "vivo_8662_64")
	q.Set("device_platform", "android")
	q.Set("device_type", "PGT-AN10")
	q.Set("os_version", "12")
	q.Set("update_version_code", "74332")
	q.Set("iid", identity.InstallID)
	q.Set("cdid", "0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9")
	q.Set("device_id", identity.DeviceID)

	payload := map[string]any{
		"biz_param": map[string]any{
			"detail_page_version": 0, "device_level": 1, "disable_digg_stat": false,
			"disable_video_relate_book": false, "need_all_video_definition": true,
			"need_mp4_align": false, "use_os_player": false, "use_server_dns": false, "video_platform": 1024,
		},
		"mixed_video_id_map": map[string]any{"1004": []string{vid}},
		"dr_scene":           "player",
	}
	body, _ := json.Marshal(payload)
	bodyMd5 := md5.Sum(body)

	now := time.Now()
	ts := uint32(now.Unix())
	signed := false
	for delta := 0; delta < 1024; delta++ {
		q.Set("_rticket", strconv.FormatInt(now.UnixMilli()+int64(delta), 10))
		query := v5EncodeQuery(q)
		queryHash := hongguoSM3([]byte(query))
		if v5IV(queryHash[:], bodyMd5[:], ts)&15 != 12 {
			continue
		}
		who := v5Who{
			deviceID:    v5QueryValue(query, "device_id"),
			versionName: v5QueryValue(query, "version_name"),
			osVersion:   v5QueryValue(query, "os_version"),
			deviceType:  v5QueryValue(query, "device_type"),
			channel:     v5QueryValue(query, "channel"),
			deviceBrand: "HONOR",
		}
		if who.deviceID == "" || who.versionName == "" {
			t.Fatalf("签名缺少设备字段: %+v query=%s", who, query)
		}
		f13 := v5Hash(queryHash[:], bodyMd5[:], ts)
		if f13 == nil {
			continue
		}
		// JS 里 Helios prefix 是 randomBytes(4)、Medusa message field 3 也是随机 u32。
		// 用真随机，别用时间派生，排除"字段值不规范被服务端静默拒绝"。
		heliosPrefix := make([]byte, 4)
		for i := range heliosPrefix {
			heliosPrefix[i] = byte(time.Now().UnixNano() >> uint((i+1)*7))
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+path+"?"+query, bytes.NewReader(body))
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("X-Khronos", strconv.FormatUint(uint64(ts), 10))
		req.Header.Set("X-Helios", v5Helios(ts, heliosPrefix))
		req.Header.Set("X-Medusa", v5Medusa(queryHash[:], f13, ts, who, now.UnixMilli()))
		req.Header.Set("X-SS-STUB", strings.ToUpper(hex.EncodeToString(bodyMd5[:])))
		resp, err := d.client.Do(req)
		if err != nil {
			t.Fatalf("请求失败: %v", err)
		}
		content, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		showLen := len(content)
		if showLen > 300 {
			showLen = 300
		}
		t.Logf("DELTA=%d status=%d len=%d prefix=%q", delta, resp.StatusCode, len(content), string(content[:showLen]))
		signed = true
		break
	}
	if !signed {
		t.Fatal("1024 次内无法生成 v5 签名")
	}
}

var _ = math.MaxUint32
