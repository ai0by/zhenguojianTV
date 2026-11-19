package hgv5

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

func assembleHelios(khronos, random uint32) string {
	seed := append(le32(random), []byte(appID)...)
	digest := md5Raw(seed)
	ascii := []byte(hex.EncodeToString(digest[:]))
	words := make([]uint64, 4)
	for i := 0; i < 4; i++ {
		words[i] = binary.LittleEndian.Uint64(ascii[i*8 : i*8+8])
	}
	table := diffuse(words)
	padded := heliosPad(khronos)
	var output []byte
	for off := 0; off < len(padded); off += 16 {
		chunk := padded[off : off+16]
		a := binary.LittleEndian.Uint64(chunk[:8])
		b := binary.LittleEndian.Uint64(chunk[8:])
		for _, t := range table[:34] {
			b = t ^ (a + ror64(b, 8))
			a = b ^ ror64(a, 61)
		}
		var ab [16]byte
		binary.LittleEndian.PutUint64(ab[:8], a)
		binary.LittleEndian.PutUint64(ab[8:], b)
		output = append(output, ab[:]...)
	}
	result := append(le32(random), output...)
	return base64.StdEncoding.EncodeToString(result)
}

func diffuse(words []uint64) []uint64 {
	table := []uint64{words[0]}
	b0 := words[0]
	b8 := words[1]
	queue := append([]uint64{}, words[2:]...)
	for i := uint64(0); i < 34; i++ {
		x8 := ror64(b8, 8) + b0
		x8 ^= i
		queue = append(queue, x8)
		x8 ^= ror64(b0, 61)
		table = append(table, x8)
		b0 = x8
		b8 = queue[0]
		queue = queue[1:]
	}
	return table
}

func heliosPad(khronos uint32) []byte {
	text := []byte(fmt.Sprintf("%d-%s-%s", khronos, channelID, appID))
	fill := byte(16 - (len(text) % 16))
	size := ((len(text) + 1 + 15) / 16) * 16
	out := make([]byte, size)
	for i := range out {
		out[i] = fill
	}
	copy(out, text)
	return out
}
