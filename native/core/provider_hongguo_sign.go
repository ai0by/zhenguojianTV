package core

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func newHongguoDeviceID() string {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return strconv.FormatUint(1_000_000_000_000_000_000+binary.BigEndian.Uint64(random[:])%8_000_000_000_000_000_000, 10)
}

// hongguoSignVersionName 是签名里参与指纹计算的客户端版本号，
// 必须与 query 中的 version_name 保持一致（两者共用 hongguoAppVersionName）。
const hongguoSignVersionName = hongguoAppVersionName

// 注意不是常见的"20 字节固定密钥异或"版本，而是 RC4 变体 + 位反转。
func hongguoXGorgon(query string, body []byte, timestamp, random uint32) string {
	queryHash := hongguoMD5Sum([]byte(query))
	payload := make([]byte, 0, 20)
	payload = append(payload, queryHash[:4]...)
	if body != nil {
		bodyHash := hongguoMD5Sum(body)
		payload = append(payload, bodyHash[:4]...)
	} else {
		payload = append(payload, 0, 0, 0, 0)
	}
	payload = append(payload, 0, 0, 0, 0)
	var sdk [4]byte
	binary.LittleEndian.PutUint32(sdk[:], 67503104)
	payload = append(payload, sdk[:]...)
	var stamp [4]byte
	binary.BigEndian.PutUint32(stamp[:], timestamp)
	payload = append(payload, stamp[:]...)
	key := [8]byte{0x4a, 0x40, 0x16, byte(random >> 8), 0x47, 0x6c, 0x01, byte(random)}
	out := hongguoRC4Variant(payload, key[:])
	for index := range out {
		value := bits8Rotate4(out[index])
		next := out[0]
		if index+1 < len(out) {
			next = out[index+1]
		}
		out[index] = ^(reverseBits8(next^value) ^ 20)
	}
	signature := make([]byte, 0, 6+len(out))
	signature = append(signature, 0x84, 0x04, byte(random), byte(random>>8), 0x40, 0x01)
	signature = append(signature, out...)
	return hex.EncodeToString(signature)
}

func bits8Rotate4(value byte) byte {
	return value>>4 | value<<4
}

func reverseBits8(value byte) byte {
	var result byte
	for index := 0; index < 8; index++ {
		result = result<<1 | (value>>uint(index))&1
	}
	return result
}

// 密钥调度只做单向赋值（s[i] = s[j] 且不回写 s[j]），与标准 RC4 不同。
func hongguoRC4Variant(data, key []byte) []byte {
	var state [256]byte
	for index := range state {
		state[index] = byte(index)
	}
	position := 0
	for index := 0; index < 256; index++ {
		position = (position + int(state[index]) + int(key[index%len(key)])) & 0xff
		state[index] = state[position]
	}
	left, right := 0, 0
	out := make([]byte, len(data))
	for index, value := range data {
		left++
		first := int(state[left&0xff])
		right += first
		second := int(state[right&0xff])
		state[left&0xff] = byte(second)
		out[index] = value ^ state[(second+second)&0xff]
	}
	return out
}

// x-medusa / x-helios 是必需项（裸请求约 3~6/10，补齐后 10/10）；
// rawQuery 必须是实际发出的 query（含 _rticket）。
func signHongguoAppRequest(request *http.Request, body []byte, rawQuery, deviceID string, now time.Time) {
	khronos := uint32(now.Unix())
	random := hongguoRandomUint32()
	request.Header.Set("X-Khronos", strconv.FormatUint(uint64(khronos), 10))
	request.Header.Set("X-Gorgon", hongguoXGorgon(rawQuery, body, khronos, random))
	request.Header.Set("X-SS-Req-Ticket", strconv.FormatInt(now.UnixMilli(), 10))
	request.Header.Set("X-Tt-Request-Tag", "t=0;n=0")
	request.Header.Set("Passport-Sdk-Version", "50561")
	request.Header.Set("X-Vc-Bdturing-Sdk-Version", "3.7.2.cn")
	if body != nil {
		request.Header.Set("X-SS-Stub", fmt.Sprintf("%X", hongguoMD5Sum(body)))
	}
	request.Header.Set("X-Argus", base64.StdEncoding.EncodeToString(hongguoLe32(khronos)))
	request.Header.Set("X-Ladon", base64.StdEncoding.EncodeToString(hongguoBe32(khronos)))
	request.Header.Set("X-Helios", hongguoHelios(khronos, hongguoRandomUint32()))
	if medusa := hongguoBuildMedusa(request.URL.String(), body, khronos, deviceID, hongguoSignVersionName, newHongguoMedusaRandom()); medusa != "" {
		request.Header.Set("X-Medusa", medusa)
	}
}

func hongguoBe32(value uint32) []byte {
	encoded := make([]byte, 4)
	binary.BigEndian.PutUint32(encoded, value)
	return encoded
}
