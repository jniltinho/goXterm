package rfx

import "math/bits"

// BitStream provides bit-level reading from a byte slice.
// It uses a 32-bit accumulator for efficient bit extraction.
// Bits are read MSB-first (most significant bit first).
type BitStream struct {
	data      []byte
	bytePos   int
	acc       uint32 // 32-bit lookahead accumulator, left-aligned
	bitsInAcc int    // bits available in accumulator
}

// NewBitStream creates a new bit stream reader
func NewBitStream(data []byte) *BitStream {
	bs := &BitStream{
		data:    data,
		bytePos: 0,
	}
	bs.refill()
	return bs
}

// refill loads more bytes into the accumulator (left-aligned)
func (bs *BitStream) refill() {
	for bs.bitsInAcc <= 24 && bs.bytePos < len(bs.data) {
		// Shift accumulator left by 8, add new byte in LSB position
		bs.acc |= uint32(bs.data[bs.bytePos]) << (24 - bs.bitsInAcc)
		bs.bytePos++
		bs.bitsInAcc += 8
	}
}

// ReadBits reads n bits (up to 25) from the stream
func (bs *BitStream) ReadBits(n int) uint32 {
	if n == 0 {
		return 0
	}
	if n > bs.bitsInAcc {
		bs.refill()
	}
	if n > bs.bitsInAcc {
		// Not enough bits remaining, return what we have
		if bs.bitsInAcc == 0 {
			return 0
		}
		result := bs.acc >> (32 - bs.bitsInAcc)
		bs.bitsInAcc = 0
		bs.acc = 0
		return result
	}

	result := bs.acc >> (32 - n)
	bs.acc <<= n
	bs.bitsInAcc -= n
	return result
}

// ReadBit reads a single bit
func (bs *BitStream) ReadBit() uint32 {
	return bs.ReadBits(1)
}

// CountLeadingZeros counts consecutive zero bits (unary prefix) until a 1 is found.
// The terminating 1 bit is consumed.
func (bs *BitStream) CountLeadingZeros() int {
	count := 0
	for {
		if bs.bitsInAcc == 0 {
			bs.refill()
			if bs.bitsInAcc == 0 {
				return count
			}
		}

		// Check top bit
		if (bs.acc & 0x80000000) != 0 {
			// Found a 1 bit, consume it and return
			bs.acc <<= 1
			bs.bitsInAcc--
			return count
		}

		// Consume the 0 bit
		bs.acc <<= 1
		bs.bitsInAcc--
		count++

		// Safety limit to prevent infinite loops on malformed data
		if count > 32000 {
			return count
		}
	}
}

// CountLeadingOnes counts consecutive one bits until a 0 is found.
// The terminating 0 bit is consumed.
func (bs *BitStream) CountLeadingOnes() int {
	count := 0
	for {
		if bs.bitsInAcc == 0 {
			bs.refill()
			if bs.bitsInAcc == 0 {
				return count
			}
		}

		// Check top bit
		if (bs.acc & 0x80000000) == 0 {
			// Found a 0 bit, consume it and return
			bs.acc <<= 1
			bs.bitsInAcc--
			return count
		}

		// Consume the 1 bit
		bs.acc <<= 1
		bs.bitsInAcc--
		count++

		if count > 32000 {
			return count
		}
	}
}

// RemainingBits returns approximate remaining bits in stream
func (bs *BitStream) RemainingBits() int {
	return (len(bs.data)-bs.bytePos)*8 + bs.bitsInAcc
}

// RLGRDecode decodes RLGR-encoded data into coefficient array.
// mode: RLGR1 or RLGR3, from the context (the same for all three components)
// output: pre-allocated int16 slice of size TilePixels (4096)
//
// rustxterm: rewritten after MS-RDPRFX 3.1.8.1.7.3 and FreeRDP's rfx_rlgr_decode. The stream ends
// when the bits run out in the middle of a code (encoders pad with zero bits) and the coefficients
// not reached stay zero; upstream reported that normal end as an error, so real Windows tiles
// failed. Values past the 4,096th coefficient are ignored.
func RLGRDecode(data []byte, mode int, output []int16) error {
	if len(output) < TilePixels {
		return ErrBufferTooSmall
	}
	for i := range output[:TilePixels] {
		output[i] = 0
	}
	b := rlgrBits{data: data, left: 8 * len(data)}
	k, kp := uint32(1), uint32(1<<LSGR)
	kr, krp := uint32(1), uint32(1<<LSGR)
	idx := 0
	up := func(p *uint32, by uint32) { *p = min(*p+by, KPMAX) }
	down := func(p *uint32, by uint32) { *p -= min(*p, by) }
	put := func(v int16) {
		if idx < TilePixels {
			output[idx] = v
		}
		idx++
	}
	// grCode reads a Golomb-Rice code (unary prefix of ones, then kr bits) and adapts kr.
	grCode := func() (uint32, bool) {
		vk, ok := b.run(1)
		if !ok || b.left < int(kr) {
			return 0, false
		}
		code := b.read(int(kr)) | uint32(vk)<<kr // #nosec G115 -- vk is bounded by the data size
		if vk == 0 {
			down(&krp, 2)
		} else if vk != 1 {
			up(&krp, uint32(vk)) // #nosec G115
		}
		kr = krp >> LSGR
		return code, true
	}
	for idx < TilePixels && b.left > 0 {
		if k != 0 { // run-length mode: runs of zeros, then one nonzero value
			vk, ok := b.run(0)
			if !ok {
				break
			}
			for ; vk > 0; vk-- {
				idx += 1 << k
				up(&kp, UP_GR)
				k = kp >> LSGR
			}
			if b.left < int(k) {
				break
			}
			idx += int(b.read(int(k)))
			if b.left < 1 {
				break
			}
			sign := b.read(1)
			code, ok := grCode()
			if !ok {
				break
			}
			down(&kp, DN_GR)
			k = kp >> LSGR
			v := int16(code + 1) // #nosec G115 -- truncation as FreeRDP's INT16 cast
			if sign != 0 {
				v = -v
			}
			put(v)
			continue
		}
		code, ok := grCode() // Golomb-Rice mode
		if !ok {
			break
		}
		if mode == RLGR1 {
			if code == 0 {
				up(&kp, UQ_GR)
			} else {
				down(&kp, DQ_GR)
			}
			k = kp >> LSGR
			put(twoMagSign(code))
			continue
		}
		// RLGR3: the code is the sum of two values; the first is sent in as many bits as the code
		n := bits.Len32(code)
		if b.left < n {
			break
		}
		v1 := b.read(n)
		v2 := code - v1
		if v1 != 0 && v2 != 0 {
			down(&kp, 2*DQ_GR)
		} else if v1 == 0 && v2 == 0 {
			up(&kp, 2*UQ_GR)
		}
		k = kp >> LSGR
		put(twoMagSign(v1))
		put(twoMagSign(v2))
	}
	return nil
}

// twoMagSign decodes a value whose sign is the low bit: 2*mag - sign.
func twoMagSign(v uint32) int16 {
	if v&1 != 0 {
		return -int16((v + 1) >> 1) // #nosec G115
	}
	return int16(v >> 1) // #nosec G115
}

// rlgrBits reads MSB-first bits; left counts the bits that remain.
type rlgrBits struct {
	data []byte
	pos  int
	left int
}

func (b *rlgrBits) read(n int) uint32 {
	var v uint32
	for ; n > 0; n-- {
		v = v<<1 | uint32(b.data[b.pos>>3]>>(7-b.pos&7)&1)
		b.pos++
		b.left--
	}
	return v
}

// run counts consecutive bits equal to bit and consumes the terminating opposite bit; false when
// the data ends first.
func (b *rlgrBits) run(bit uint32) (int, bool) {
	n := 0
	for b.left > 0 {
		if b.read(1) != bit {
			return n, true
		}
		n++
	}
	return n, false
}
