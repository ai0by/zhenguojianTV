package hgv5

import (
	"encoding/binary"
	"math"
)

func varint(value uint64) []byte {
	var out []byte
	for value > 127 {
		out = append(out, byte(value&127)|128)
		value >>= 7
	}
	return append(out, byte(value))
}

func protoKey(tag uint32, wire byte) []byte {
	return varint((uint64(tag) << 3) | uint64(wire))
}

func protoBytes(tag uint32, body []byte) []byte {
	if len(body) == 0 {
		return nil
	}
	out := protoKey(tag, 2)
	out = append(out, varint(uint64(len(body)))...)
	return append(out, body...)
}

func protoString(tag uint32, s string) []byte {
	return protoBytes(tag, []byte(s))
}

func protoSInt(tag uint32, value int64) []byte {
	out := protoKey(tag, 0)
	return append(out, varint(zigzag(value))...)
}

func protoFloat(tag uint32, value float32) []byte {
	out := protoKey(tag, 5)
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], math.Float32bits(value))
	return append(out, b[:]...)
}
