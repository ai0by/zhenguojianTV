package hgv5

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// SignGET builds the query string and portable signature headers for one GET.
// pairs are encoded in the given order. ts and _rticket are appended.
// A nil entropy draws fresh random fields. Header values are not logged here.
func SignGET(pairs [][2]string, nowMillis int64, deviceID, versionName string, entropy *Entropy) (string, map[string]string) {
	return signRequest("/reading/bookapi/search/tab/v", pairs, nowMillis, deviceID, versionName, nil, entropy)
}

// SignPOST signs one JSON body. The returned headers include the body stub.
// The caller must send the same body bytes that were signed.
func SignPOST(path string, pairs [][2]string, nowMillis int64, deviceID, versionName string, body []byte, entropy *Entropy) (string, map[string]string) {
	return signRequest(path, pairs, nowMillis, deviceID, versionName, body, entropy)
}

func signRequest(path string, pairs [][2]string, nowMillis int64, deviceID, versionName string, body []byte, entropy *Entropy) (string, map[string]string) {
	ent := Entropy{}
	if entropy != nil {
		ent = *entropy
	} else {
		ent = drawEntropy(nowMillis)
	}
	hasBody := body != nil
	bodyMD5 := make([]byte, 16)
	if hasBody {
		sum := md5Raw(body)
		bodyMD5 = sum[:]
	}
	base := uint64(nowMillis)
	var query string
	var ticket uint64
	var khronos uint32
	for offset := uint64(0); offset < 32; offset++ {
		ticket = base + offset
		khronos = uint32(ticket / 1000)
		withTicket := append([][2]string{}, pairs...)
		withTicket = append(withTicket,
			[2]string{"ts", strconv.FormatUint(uint64(khronos), 10)},
			[2]string{"_rticket", strconv.FormatUint(ticket, 10)},
		)
		query = encodeQuery(withTicket)
		sum := sm3Sum([]byte(query))
		if branchOf(sum[:], bodyMD5, le32(khronos)) != 1 {
			break
		}
	}
	if ent.NowSecs == 0 {
		ent.NowSecs = int64(khronos)
	}
	// x-medusa hashes this URL. The service accepts it with this host on
	// requests sent to the fqnovel hosts as well (observed 2026-10-24), so it
	// does not follow the origin a request goes to.
	rawURL := "https://reading.snssdk.com" + path + "?" + query
	headers := map[string]string{
		"Accept":                    "application/json; charset=utf-8,application/x-protobuf",
		"x-xs-from-web":             "0",
		"x-ss-req-ticket":           strconv.FormatUint(ticket, 10),
		"x-tt-request-tag":          "t=0;n=0",
		"sdk-version":               "2",
		"passport-sdk-version":      "50561",
		"x-vc-bdturing-sdk-version": "3.7.2.cn",
		"x-khronos":                 strconv.FormatUint(uint64(khronos), 10),
		"x-ladon":                   base64.StdEncoding.EncodeToString(be32(khronos)),
		"x-argus":                   base64.StdEncoding.EncodeToString(le32(khronos)),
		"x-gorgon":                  xGorgon(query, body, hasBody, khronos, ent.GorgonRandom),
		"x-helios":                  assembleHelios(khronos, ent.HeliosRandom),
		"x-medusa":                  assembleMedusa(rawURL, body, hasBody, khronos, deviceID, versionName, ent),
		"x-tt-dt":                   "",
	}
	if hasBody {
		headers["Content-Type"] = "application/json; charset=UTF-8"
		sum := md5Raw(body)
		headers["x-ss-stub"] = strings.ToUpper(hex.EncodeToString(sum[:]))
	}
	return query, headers
}

func encodeQuery(pairs [][2]string) string {
	parts := make([]string, len(pairs))
	for i, pair := range pairs {
		parts[i] = encodeComponent(pair[0]) + "=" + encodeComponent(pair[1])
	}
	return strings.Join(parts, "&")
}

func encodeComponent(s string) string {
	const unreserved = "-_.!~*'()"
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || strings.ContainsRune(unreserved, rune(c)) {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func drawEntropy(nowMillis int64) Entropy {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	n := binary.LittleEndian.Uint64(buf[:8])
	return Entropy{
		EnvLaunch:     100 + int64(n%21),
		EnvPID:        10001 + int64(binary.LittleEndian.Uint16(buf[8:10])%2000),
		NowSecs:       nowMillis / 1000,
		MessageRandom: binary.LittleEndian.Uint32(buf[0:4]),
		Random:        binary.LittleEndian.Uint32(buf[4:8]),
		PackedRandom:  binary.LittleEndian.Uint32(buf[8:12]),
		GorgonRandom:  uint32(binary.LittleEndian.Uint16(buf[12:14])),
		HeliosRandom:  binary.LittleEndian.Uint32(buf[12:16]),
	}
}
