package hgv5

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
)

type Entropy struct {
	EnvLaunch     int64
	EnvPID        int64
	NowSecs       int64
	MessageRandom uint32
	Random        uint32
	PackedRandom  uint32
	GorgonRandom  uint32
	HeliosRandom  uint32
}

func xmxor(data, key []byte) []byte {
	n := len(data)
	encoded := make([]byte, n)
	for i := 0; i < n; i++ {
		at := (i * 4) & 28
		d0 := key[at]
		d1 := key[at+1]
		d2 := byte((uint32((data[i]<<4)|(data[i]>>4)) + uint32(d0)))
		d2 = ^d2 ^ d1
		d2 = (d2 << 3) | (d2 >> 5)
		d2 += d1
		d2 ^= d0
		encoded[n-i-1] = ^d2
	}
	last := encoded[n-1] ^ encoded[n-2]
	first := encoded[0]
	encoded[0] = (^last) + first
	encoded[1] = (encoded[0] ^ encoded[n-1] ^ 254) + encoded[1]
	shifted := (encoded[1] << 3) | (encoded[1] >> 5)
	fix2 := (last - first) ^ (shifted ^ 2)
	encoded[2] += fix2
	for i := 0; i < n-4; i++ {
		sh := (encoded[i+2] << 3) | (encoded[i+2] >> 5)
		temp := sh ^ encoded[i+1] ^ byte(i+3)
		encoded[i+3] = (^temp) + encoded[i+3]
	}
	encoded[n-1] ^= encoded[n-2]
	var sum uint32
	for _, item := range encoded[1:] {
		sum += uint32(item)
	}
	encoded[0] = (encoded[0] ^ encoded[1]) + byte(sum)
	return encoded
}

func keyHash(signKey []byte, random uint32) ([32]byte, [4]byte) {
	buf := append([]byte{}, signKey...)
	buf = append(buf, le32(random)...)
	buf = append(buf, signKey...)
	hash := sm3Sum(buf)
	d1 := byte((random >> 16) & 255)
	d2 := (uint32(d1) << 11) | (random >> 24)
	d2 ^= uint32(d1) >> 5
	d2 ^= uint32(d1)
	d2 = ^d2
	var seed [4]byte
	copy(seed[:], le32(d2))
	return hash, seed
}

func deviceProto(deviceID string) []byte {
	var out []byte
	out = append(out, protoSInt(1, 1)...)
	out = append(out, protoSInt(2, 2)...)
	out = append(out, protoString(3, appID)...)
	out = append(out, protoString(4, deviceID)...)
	out = append(out, protoString(5, "Ai6svO3PyrwDOUSmO6ZcResxu")...)
	out = append(out, protoString(6, "!noperm!")...)
	out = append(out, protoSInt(7, -888888)...)
	out = append(out, protoSInt(8, -888888)...)
	out = append(out, protoSInt(9, 3)...)
	out = append(out, protoSInt(10, -888888)...)
	out = append(out, protoString(11, "!notset!")...)
	out = append(out, protoString(12, "Asia/Shanghai,8")...)
	out = append(out, protoString(13, "zh_CN")...)
	out = append(out, protoSInt(14, 4)...)
	out = append(out, protoFloat(16, 255.24993896484375)...)
	out = append(out, protoFloat(17, 35.58599090576172)...)
	out = append(out, protoFloat(18, 3.467449188232422)...)
	out = append(out, protoFloat(19, 3.467449188232422)...)
	out = append(out, protoFloat(20, 255.1754913330078)...)
	out = append(out, protoFloat(21, 42.17544174194336)...)
	out = append(out, protoString(22, "16")...)
	out = append(out, protoSInt(23, 41)...)
	out = append(out, protoSInt(24, 36)...)
	out = append(out, protoSInt(25, 1728388016635)...)
	out = append(out, protoSInt(26, 1728388016635)...)
	out = append(out, protoSInt(27, 1728388016635)...)
	out = append(out, protoSInt(28, 1728388016637)...)
	out = append(out, protoSInt(29, -1)...)
	out = append(out, protoString(30, "25053RT47C")...)
	out = append(out, protoString(31, "Redmi")...)
	out = append(out, protoString(32, "25053RT47C")...)
	out = append(out, protoString(33, "25053RT47C")...)
	out = append(out, protoString(34, "Xiaomi")...)
	out = append(out, protoString(35, "Redmi")...)
	out = append(out, protoString(36, "Redmi")...)
	out = append(out, protoSInt(38, 31)...)
	return out
}

func assembleMedusa(rawURL string, body []byte, hasBody bool, khronos uint32, deviceID, versionName string, entropy Entropy) string {
	bodyMD5 := [16]byte{}
	if hasBody {
		bodyMD5 = md5Raw(body)
	}
	queryRaw := ""
	if i := indexByte(rawURL, '?'); i >= 0 {
		queryRaw = rawURL[i+1:]
	}
	querySM3 := sm3Sum([]byte(queryRaw))
	ts := le32(khronos)
	queryBodyTS := hashF13(querySM3[:], bodyMD5[:], ts, khronos)
	check := (uint32(querySM3[0]&63) << 14) | 0x18000001 | (uint32(queryBodyTS[0]&63) << 8)

	nested := append(protoSInt(1, 111), protoSInt(2, 10)...)
	nested = append(nested, protoSInt(3, 694367)...)
	nested = append(nested, protoSInt(5, 586952199)...)
	nested13 := append(protoSInt(1, entropy.NowSecs), protoSInt(2, -2)...)
	nested13 = append(nested13, protoSInt(4, 200)...)
	env := protoSInt(1, entropy.EnvLaunch)
	env = append(env, protoSInt(2, 146331399)...)
	env = append(env, protoSInt(3, 146331396)...)
	env = append(env, protoSInt(5, 7)...)
	env = append(env, protoString(6, "v04.06.04.03-bugfix")...)
	env = append(env, protoSInt(7, entropy.EnvPID)...)
	env = append(env, protoBytes(12, deviceProto(deviceID))...)
	env = append(env, protoBytes(13, nested13)...)
	env = append(env, protoString(14, versionName)...)

	qh := append([]byte(queryRaw), bodyMD5[:]...)
	qh = append(qh, []byte("none")...)
	queryHash := sm3Sum(qh)

	fixed1, _ := hex.DecodeString("f7e85ffad7d7dc3bd62ac87057cf6118")
	fixed2, _ := hex.DecodeString("4001000000000000")
	message := protoBytes(1, fixed1)
	message = append(message, protoSInt(2, 3)...)
	message = append(message, protoSInt(3, int64(entropy.MessageRandom))...)
	message = append(message, protoString(4, appID)...)
	message = append(message, protoString(5, deviceID)...)
	message = append(message, protoString(6, channelID)...)
	message = append(message, protoString(7, versionName)...)
	message = append(message, protoString(8, "v04.06.04-ml-android")...)
	message = append(message, protoSInt(9, 67503104)...)
	message = append(message, protoBytes(10, fixed2)...)
	message = append(message, protoSInt(12, int64(khronos))...)
	message = append(message, protoBytes(13, queryBodyTS)...)
	message = append(message, protoBytes(14, querySM3[:6])...)
	message = append(message, protoBytes(15, nested)...)
	message = append(message, protoString(16, "AXYQOS6n2m60x1fVZHIrH3iol")...)
	message = append(message, protoSInt(17, int64(khronos))...)
	message = append(message, protoBytes(19, queryHash[:])...)
	message = append(message, protoString(20, "none")...)
	message = append(message, protoSInt(21, 312)...)
	message = append(message, protoBytes(23, env)...)
	message = append(message, protoString(24, `{"cmr":16777216,"cmr2":16777216,"un_h":1879194040,"vpn":0,"kd":0,"fkd":3672518972,"pd":-1872573247,"dyn":"","do":0,"tk":true}`)...)

	hash, seed := keyHash(medusaSignKey(), entropy.Random)
	transformed := xmxor(message, hash[:])
	prefixBytes := append([]byte{}, fixed2...)
	prefixBytes = append(prefixBytes, transformed...)
	transformed = prefixBytes
	for i, j := 0, len(transformed)-1; i < j; i, j = i+1, j-1 {
		transformed[i], transformed[j] = transformed[j], transformed[i]
	}
	for i := range transformed {
		transformed[i] ^= seed[(^i)&3]
	}
	packed := []byte{0x35}
	packed = append(packed, le32(entropy.PackedRandom)...)
	packed = append(packed, le32(check)...)
	packed = append(packed, transformed...)
	packed = append(packed, byte(entropy.Random>>16), byte(entropy.Random>>24))

	encrypted := newAESV3(medusaAESKey(), khronos).encrypt(packed, medusaAESIV())
	version, _ := hex.DecodeString("03000000f7e85ffad7d7dc3bd62ac87057cf6118")
	var prefix [20]byte
	for i := 0; i < 5; i++ {
		v := binary.LittleEndian.Uint32(version[i*4 : i*4+4])
		copy(prefix[i*4:i*4+4], le32(v^khronos))
	}
	out := append([]byte{}, prefix[:]...)
	out = append(out, byte(entropy.Random&255), byte((entropy.Random>>8)&255), 0, 1)
	out = append(out, encrypted...)
	return base64.StdEncoding.EncodeToString(out)
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
