package hgv5

import "encoding/binary"

// sm3Sum is GM/T 0004-2012. The digest of "abc" is the published test vector.
func sm3Sum(msg []byte) [32]byte {
	h := [8]uint32{
		0x7380166f, 0x4914b2b9, 0x172442d7, 0xda8a0600,
		0xa96f30bc, 0x163138aa, 0xe38dee4d, 0xb0fb0e4e,
	}
	padded := sm3Pad(msg)
	var w [68]uint32
	var wp [64]uint32
	for off := 0; off < len(padded); off += 64 {
		block := padded[off : off+64]
		for i := 0; i < 16; i++ {
			w[i] = binary.BigEndian.Uint32(block[i*4 : i*4+4])
		}
		for j := 16; j < 68; j++ {
			x := w[j-16] ^ w[j-9] ^ rol32(w[j-3], 15)
			w[j] = sm3P1(x) ^ rol32(w[j-13], 7) ^ w[j-6]
		}
		for j := 0; j < 64; j++ {
			wp[j] = w[j] ^ w[j+4]
		}
		a, b, c, d := h[0], h[1], h[2], h[3]
		e, f, g, hh := h[4], h[5], h[6], h[7]
		for j := 0; j < 64; j++ {
			tj := uint32(0x7a879d8a)
			if j < 16 {
				tj = 0x79cc4519
			}
			ss1 := rol32(rol32(a, 12)+e+rol32(tj, uint32(j)), 7)
			ss2 := ss1 ^ rol32(a, 12)
			tt1 := sm3FF(j, a, b, c) + d + ss2 + wp[j]
			tt2 := sm3GG(j, e, f, g) + hh + ss1 + w[j]
			d = c
			c = rol32(b, 9)
			b = a
			a = tt1
			hh = g
			g = rol32(f, 19)
			f = e
			e = sm3P0(tt2)
		}
		h[0] ^= a
		h[1] ^= b
		h[2] ^= c
		h[3] ^= d
		h[4] ^= e
		h[5] ^= f
		h[6] ^= g
		h[7] ^= hh
	}
	var out [32]byte
	for i := 0; i < 8; i++ {
		binary.BigEndian.PutUint32(out[i*4:], h[i])
	}
	return out
}

func sm3Pad(msg []byte) []byte {
	bitLen := uint64(len(msg)) * 8
	out := make([]byte, 0, len(msg)+72)
	out = append(out, msg...)
	out = append(out, 0x80)
	for (len(out)+8)%64 != 0 {
		out = append(out, 0)
	}
	var lenb [8]byte
	binary.BigEndian.PutUint64(lenb[:], bitLen)
	return append(out, lenb[:]...)
}

func sm3FF(j int, x, y, z uint32) uint32 {
	if j < 16 {
		return x ^ y ^ z
	}
	return (x & y) | (x & z) | (y & z)
}

func sm3GG(j int, x, y, z uint32) uint32 {
	if j < 16 {
		return x ^ y ^ z
	}
	return (x & y) | (^x & z)
}

func sm3P0(x uint32) uint32 { return x ^ rol32(x, 9) ^ rol32(x, 17) }

func sm3P1(x uint32) uint32 { return x ^ rol32(x, 15) ^ rol32(x, 23) }
