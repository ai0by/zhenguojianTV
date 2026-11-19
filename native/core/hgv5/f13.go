package hgv5

import (
	"encoding/binary"
	"encoding/hex"
)

var branch0IV = [6]uint32{0xc4a78580, 0xb3c0fd39, 0xc58c5686, 0xc9aa3ba7, 0xf5a7adf2, 0x963c2ed1}

var branch0TT = [64]uint32{
	0xebb64faf, 0x07aadcc2, 0xcf3187bf, 0xe01138ff, 0x6d0bfcff, 0x5a30a3be, 0xb41ad638, 0x34180eb8,
	0xf233eb6f, 0xb1a584cc, 0xccc30dc7, 0x47d1db51, 0xd55653de, 0x70a84fa1, 0x57473c12, 0xf76f0288,
	0x2c077f0a, 0xda0dcad0, 0xfbb86f6c, 0xfdc4cf00, 0x688a020d, 0xe676c6a6, 0x8cd6338b, 0x1a3c8d0e,
	0xcce8b06b, 0x6ad0ed0b, 0xa0522717, 0xdc71ac83, 0x2285db71, 0xd5b4dda6, 0x736f8650, 0x6560306c,
	0x617ce2a6, 0xe423417e, 0x0a40e143, 0x544e4032, 0x88dffb2a, 0x716c1ae0, 0x4c467a88, 0x05b23bb3,
	0xe1d0b866, 0xbaa3dcb8, 0xae3374d3, 0xc3381a50, 0x1702f75b, 0xfe6da368, 0xf0b4cf48, 0x4e0ffbb8,
	0x72aad10d, 0x26c53a3d, 0xf2bce0f6, 0xb4557581, 0x4a257fdd, 0x8c3182a2, 0xab0b3b86, 0x3d5dfb14,
	0x4f103634, 0xd37b52d7, 0x444eff16, 0xeb0a33d1, 0x6ca86f6e, 0x00284ba7, 0x08387cfa, 0x5fb37586,
}

var branch0Init = [8]uint32{
	0x7aba4fc8, 0x67166507, 0x6403fa00, 0x340f512f,
	984304912, 3005047866, 2874125293, 2152413264,
}

var branch0Rounds = [6][11]uint32{
	{101, 5, 7, 6, 3, 2, 1, 0, 5, 4, 3},
	{96, 0, 6, 7, 5, 3, 2, 1, 5, 4, 4},
	{96, 7, 6, 2, 1, 4, 0, 5, 4, 3, 5},
	{99, 3, 6, 2, 4, 5, 1, 0, 0, 7, 6},
	{96, 0, 5, 6, 7, 3, 1, 2, 5, 4, 4},
	{100, 2, 0, 3, 5, 4, 6, 7, 2, 1, 5},
}

func branchOf(query, bodyMD5, ts []byte) uint32 {
	iv := getIV(getIV(getIV(0x20230928, query), bodyMD5), ts)
	low := iv & 15
	return low - ((low*171)>>9)*3
}

func hashF13(querySM3, bodyMD5, ts []byte, khronos uint32) []byte {
	iv := getIV(getIV(getIV(0x20230928, querySM3), bodyMD5), ts)
	low := iv & 15
	ivV0 := (low * 171) >> 9
	branch := low - ivV0*3
	if branch == 2 {
		return branch2F13(iv, querySM3, bodyMD5, ts, khronos)
	}
	if branch != 0 {
		out := append([]byte{}, querySM3[:16]...)
		out = append(out, bodyMD5...)
		summed := append(append([]byte{}, querySM3...), bodyMD5...)
		out = append(out, le32(sumMD5(summed))...)
		return out
	}
	return branch0F13(querySM3, bodyMD5, ts, khronos, ivV0)
}

func expandWords(data []byte) []uint32 {
	di := make([]uint32, 0, 16+112)
	for off := 0; off+4 <= len(data); off += 4 {
		di = append(di, binary.BigEndian.Uint32(data[off:off+4]))
	}
	di0 := di[0]
	for i := 0; i < 112; i++ {
		di1 := di[i+1]
		di14 := di[i+14]
		r1 := rol32(di1, 14) ^ rol32(di1, 25) ^ (di1 >> 3)
		r2 := rol32(di14, 13) ^ rol32(di14, 15) ^ (di14 >> 10)
		di = append(di, di0+di[i+9]+r1+r2)
		di0 = di1
	}
	return di
}

func mixRounds(state []uint32, rounds [11]uint32, di, tt []uint32, ivV1 uint32) []uint32 {
	for i := uint32(0); i < rounds[0]; i++ {
		base := ivV1 + i
		w := di[base&127]
		idx := func(k int) int { return int(rounds[k]) }
		n1 := ((state[idx(3)] ^ state[idx(4)]) & state[idx(1)]) ^ state[idx(3)]
		n2 := rol32(state[idx(1)], 26) ^ rol32(state[idx(1)], 21) ^ rol32(state[idx(1)], 7)
		n4 := w + n1 + n2 + tt[base&63] + state[idx(5)]
		n5 := rol32(state[idx(2)], 30) ^ rol32(state[idx(2)], 19) ^ rol32(state[idx(2)], 10)
		n6 := (state[idx(2)] & state[idx(6)]) | ((state[idx(2)] | state[idx(6)]) & state[idx(7)])
		n7 := n5 + n6
		old := state[idx(9)]
		last := state[len(state)-1]
		state = append([]uint32{last}, state[:len(state)-1]...)
		state[idx(10)] = n7 + n4
		state[idx(8)] = old + n4
	}
	return state
}

func branch0F13(querySM3, bodyMD5, ts []byte, khronos, ivV0 uint32) []byte {
	ivV1 := branch0IV[min(int(ivV0), len(branch0IV)-1)]
	countV1 := (ivV1 + khronos + 1) & 255
	countV2 := ivV1 + khronos
	tt := make([]uint32, len(branch0TT))
	for i, v := range branch0TT {
		tt[i] = ror32(v, countV1)
	}
	n0 := (countV2 + 2) & 7
	seed := []byte{0xfa, 0x45, 0x61, 0xd7}
	pad := make([]byte, len(seed))
	for i, v := range seed {
		combined := uint16(v) | (uint16(v) << 8)
		pad[i] = byte((combined >> n0) & 255)
	}
	data := append([]byte{}, querySM3...)
	data = append(data, bodyMD5...)
	data = append(data, ts...)
	data = append(data, pad...)
	tail, _ := hex.DecodeString("00000000000001a0")
	data = append(data, tail...)
	di := expandWords(data)
	init := make([]uint32, len(branch0Init))
	for i, v := range branch0Init {
		init[i] = ror32(v, countV2&31)
	}
	d := mixRounds(append([]uint32{}, init...), branch0Rounds[ivV0], di, tt, ivV1)
	ret := make([]byte, 0, 32)
	for i := 0; i < 8; i++ {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], d[i]+init[i])
		ret = append(ret, b[:]...)
	}
	for i := 0; i < 16; i++ {
		ret[i] = ret[i] ^ ret[16+i]
	}
	out := append([]byte{}, ret[:16]...)
	return append(out, le32(sumMD5(ret[:16]))...)
}

func md5V3Step(kind, a, b, c, d, m, shift, constant uint32) uint32 {
	var f uint32
	switch kind {
	case 0:
		f = (b & c) | (^b & d)
	case 1:
		f = (b & d) | (c & ^d)
	case 2:
		f = b ^ c ^ d
	default:
		f = c ^ (b | ^d)
	}
	return b + rol32(a+f+m+constant, shift)
}

func md5SumV3(message []byte, countV2 uint32, orders []byte, countV1 uint32) []byte {
	sv := make([]uint32, len(branch2SV))
	for i, v := range branch2SV {
		sv[i] = ror32(v, countV1)
	}
	start := [4]uint32{
		ror32(0x79e0f2fb, countV2),
		ror32(0xc8b52570, countV2),
		ror32(0xebc2f8cd, countV2),
		ror32(0x7c104d93, countV2),
	}
	endCount := (countV2 + 6) & 255
	end := [4]uint32{
		ror32(0x19be4866, endCount),
		ror32(0xe85986b4, endCount),
		ror32(0xe19b326e, endCount),
		ror32(0x71d1d7d4, endCount),
	}
	var m [16]uint32
	for i := range m {
		m[i] = binary.LittleEndian.Uint32(message[i*4 : i*4+4])
	}
	a, b, c, d := start[0], start[1], start[2], start[3]
	run := func(kind uint32, shifts [16]uint32, offset int) {
		for i := 0; i < 16; i++ {
			phase := i & 3
			w := m[orders[offset+i]]
			k := sv[offset+i]
			var next uint32
			switch phase {
			case 0:
				next = md5V3Step(kind, a, b, c, d, w, shifts[i], k)
				a = next
			case 1:
				next = md5V3Step(kind, d, a, b, c, w, shifts[i], k)
				d = next
			case 2:
				next = md5V3Step(kind, c, d, a, b, w, shifts[i], k)
				c = next
			default:
				next = md5V3Step(kind, b, c, d, a, w, shifts[i], k)
				b = next
			}
		}
	}
	run(0, [16]uint32{7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22}, 0)
	run(1, [16]uint32{5, 9, 14, 20, 5, 9, 14, 20, 5, 9, 14, 20, 5, 9, 14, 20}, 16)
	run(2, [16]uint32{4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23}, 32)
	run(3, [16]uint32{6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21}, 48)
	ret := make([]byte, 0, 20)
	ret = append(ret, le32((start[0]+a)^end[0])...)
	ret = append(ret, le32((start[1]+b)^end[1])...)
	ret = append(ret, le32((start[2]+c)^end[2])...)
	ret = append(ret, le32((start[3]+d)^end[3])...)
	return append(ret, le32(sumMD5(ret))...)
}

func branch2F13(iv uint32, querySM3, bodyMD5, ts []byte, khronos uint32) []byte {
	v := (iv & 13) * 86
	ivV0 := ((v >> 15) & 255) + ((v >> 8) & 255)
	n1s := [5]uint32{0x8980f29b, 0xeb549c7f, 0xb08726db, 0xd40cb5e6, 0xe8f559e4}
	n1 := n1s[min(int(ivV0), len(n1s)-1)]
	countV1 := (n1 + khronos + 1) & 255
	countV2 := n1 + khronos
	shift := (countV2 + 5) & 7
	seed := []byte{0x84, 0x96, 0x77, 0x9d, 0xd4, 0x15, 0x0b, 0xf8}
	pad := make([]byte, len(seed))
	for i, value := range seed {
		combined := uint16(value) | (uint16(value) << 8)
		pad[i] = byte((combined >> shift) & 255)
	}
	input := append([]byte{}, querySM3...)
	input = append(input, bodyMD5...)
	input = append(input, ts...)
	input = append(input, pad...)
	tail, _ := hex.DecodeString("a0010000")
	input = append(input, tail...)
	all := branch2Orders()
	begin := int(ivV0) << 6
	return md5SumV3(input, countV2, all[begin:begin+64], countV1)
}
