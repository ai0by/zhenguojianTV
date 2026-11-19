package hgv5

import (
	"crypto/md5"
	"encoding/binary"
)

func rol32(value, count uint32) uint32 {
	n := count & 31
	if n == 0 {
		return value
	}
	return (value << n) | (value >> (32 - n))
}

func ror32(value, count uint32) uint32 {
	n := count & 31
	if n == 0 {
		return value
	}
	return (value >> n) | (value << (32 - n))
}

func ror64(value, count uint64) uint64 {
	n := count & 63
	if n == 0 {
		return value
	}
	return (value >> n) | (value << (64 - n))
}

func reverseBits(value byte) byte {
	var result byte
	for i := 0; i < 8; i++ {
		result = (result << 1) | ((value >> i) & 1)
	}
	return result
}

func zigzag(value int64) uint64 {
	return uint64((value << 1) ^ (value >> 63))
}

func le32(value uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], value)
	return b[:]
}

func be32(value uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], value)
	return b[:]
}

func md5Raw(value []byte) [16]byte {
	return md5.Sum(value)
}

func getIV(iv uint32, data []byte) uint32 {
	value := iv
	for i, b := range data {
		if i&1 == 0 {
			value = (value >> 4) ^ value ^ (value << 6) ^ uint32(b)
			continue
		}
		inner := (value >> 7) ^ value ^ (uint32(b) | (value << 12))
		value = ^inner
	}
	return value
}

func sumMD5(data []byte) uint32 {
	check := uint32(0x20220420)
	for i := 0; i < 12; i++ {
		var temp uint32
		if i&1 == 0 {
			temp = (check >> 3) ^ check
			check = uint32(data[i]) ^ (check << 7)
		} else {
			temp = (check >> 5) ^ check
			check = uint32(data[i]) | (check << 11)
			check = ^check
		}
		check ^= temp
	}
	return (check | 4) ^ 0x01000000
}
