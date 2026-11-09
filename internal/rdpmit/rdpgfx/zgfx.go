// rustxterm addition (not in upstream go-rdp): RDP8 bulk decompression (ZGFX, MS-RDPEGFX 2.2.5 and
// 3.1.9.1), written from the specification.

package rdpgfx

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	zgfxHistory      = 2500000 // history window shared by all segments of the channel
	zgfxSegmentMax   = 65535   // decoded bytes per segment
	zgfxSingle       = 0xE0    // ZGFX_SEGMENTED_SINGLE
	zgfxMultipart    = 0xE1    // ZGFX_SEGMENTED_MULTIPART
	zgfxCompressed   = 0x20    // PACKET_COMPRESSED in the segment header (RDP8 compression type 4)
	maxDecodedPDU    = 32 << 20
	maxSegmentsInPDU = maxDecodedPDU/zgfxSegmentMax + 1
)

// zgfxToken is one prefix code of the ZGFX token table (MS-RDPEGFX 3.1.9.1.2): a literal (match is
// false: value = base + valueBits) or a match distance (base + valueBits; 0 = unencoded bytes).
type zgfxToken struct {
	prefixLen uint8
	prefix    uint16
	valueBits uint8
	match     bool
	base      uint32
}

var zgfxTokens = [...]zgfxToken{
	{1, 0b0, 8, false, 0},
	{5, 0b10001, 5, true, 0},
	{5, 0b10010, 7, true, 32},
	{5, 0b10011, 9, true, 160},
	{5, 0b10100, 10, true, 672},
	{5, 0b10101, 12, true, 1696},
	{5, 0b11000, 0, false, 0x00},
	{5, 0b11001, 0, false, 0x01},
	{6, 0b101100, 14, true, 5792},
	{6, 0b101101, 15, true, 22176},
	{6, 0b110100, 0, false, 0x02},
	{6, 0b110101, 0, false, 0x03},
	{6, 0b110110, 0, false, 0xFF},
	{7, 0b1011100, 18, true, 54944},
	{7, 0b1011101, 20, true, 317088},
	{7, 0b1101110, 0, false, 0x04},
	{7, 0b1101111, 0, false, 0x05},
	{7, 0b1110000, 0, false, 0x06},
	{7, 0b1110001, 0, false, 0x07},
	{7, 0b1110010, 0, false, 0x08},
	{7, 0b1110011, 0, false, 0x09},
	{7, 0b1110100, 0, false, 0x0A},
	{7, 0b1110101, 0, false, 0x0B},
	{7, 0b1110110, 0, false, 0x3A},
	{7, 0b1110111, 0, false, 0x3B},
	{7, 0b1111000, 0, false, 0x3C},
	{7, 0b1111001, 0, false, 0x3D},
	{7, 0b1111010, 0, false, 0x3E},
	{7, 0b1111011, 0, false, 0x3F},
	{7, 0b1111100, 0, false, 0x40},
	{7, 0b1111101, 0, false, 0x80},
	{8, 0b10111100, 20, true, 1365664},
	{8, 0b10111101, 21, true, 2414240},
	{8, 0b11111100, 0, false, 0x0C},
	{8, 0b11111101, 0, false, 0x38},
	{8, 0b11111110, 0, false, 0x39},
	{8, 0b11111111, 0, false, 0x66},
	{9, 0b101111100, 22, true, 4511392},
	{9, 0b101111101, 23, true, 8705696},
	{9, 0b101111110, 24, true, 17094304},
}

// ZGFX is the server-to-client decompressor of one graphics channel; its history persists across
// messages.
type ZGFX struct {
	hist []byte
	pos  int // next write index in hist
	full bool
}

var errZGFX = errors.New("zgfx: malformed data")

// Decompress decodes one RDP_SEGMENTED_DATA message.
func (z *ZGFX) Decompress(in []byte) ([]byte, error) {
	if len(in) < 1 {
		return nil, errZGFX
	}
	if z.hist == nil {
		z.hist = make([]byte, zgfxHistory)
	}
	switch in[0] {
	case zgfxSingle:
		return z.segment(nil, in[1:])
	case zgfxMultipart:
		if len(in) < 7 {
			return nil, errZGFX
		}
		count := int(binary.LittleEndian.Uint16(in[1:]))
		size := int(binary.LittleEndian.Uint32(in[3:]))
		if size > maxDecodedPDU || count > maxSegmentsInPDU || size > count*zgfxSegmentMax {
			return nil, fmt.Errorf("zgfx: %d segments of %d bytes in all", count, size)
		}
		out := make([]byte, 0, min(size, zgfxSegmentMax)) // grows with real data only
		p := in[7:]
		for i := 0; i < count; i++ {
			if len(p) < 4 {
				return nil, errZGFX
			}
			n := binary.LittleEndian.Uint32(p)
			p = p[4:]
			if uint64(n) > uint64(len(p)) {
				return nil, errZGFX
			}
			var err error
			if out, err = z.segment(out, p[:n]); err != nil {
				return nil, err
			}
			if len(out) > size {
				return nil, errors.New("zgfx: segments exceed the announced size")
			}
			p = p[n:]
		}
		if len(out) != size {
			return nil, errors.New("zgfx: segments shorter than the announced size")
		}
		return out, nil
	}
	return nil, fmt.Errorf("zgfx: descriptor 0x%02X", in[0])
}

func (z *ZGFX) put(b byte) {
	z.hist[z.pos] = b
	if z.pos++; z.pos == len(z.hist) {
		z.pos, z.full = 0, true
	}
}

// segment appends one decoded RDP8_BULK_ENCODED_DATA segment to out.
func (z *ZGFX) segment(out, seg []byte) ([]byte, error) {
	if len(seg) < 1 {
		return nil, errZGFX
	}
	header, data := seg[0], seg[1:]
	start := len(out)
	if header&zgfxCompressed == 0 {
		if len(data) > zgfxSegmentMax {
			return nil, errZGFX
		}
		for _, b := range data {
			z.put(b)
		}
		return append(out, data...), nil
	}
	if len(data) < 1 {
		return nil, errZGFX
	}
	// the last byte counts the unused bits at the end of the stream
	pad := int(data[len(data)-1])
	data = data[:len(data)-1]
	r := bitReader{data: data, left: 8*len(data) - pad}
	if r.left < 0 {
		return nil, errZGFX
	}
	for r.left > 0 {
		var (
			code uint32
			n    uint8
			tok  *zgfxToken
		)
		for i := range zgfxTokens {
			t := &zgfxTokens[i]
			for n < t.prefixLen {
				b, ok := r.bits(1)
				if !ok {
					return nil, errZGFX
				}
				code, n = code<<1|b, n+1
			}
			if code == uint32(t.prefix) {
				tok = t
				break
			}
		}
		if tok == nil {
			return nil, errZGFX
		}
		v, ok := r.bits(tok.valueBits)
		if !ok {
			return nil, errZGFX
		}
		if !tok.match {
			if len(out)-start >= zgfxSegmentMax {
				return nil, errZGFX
			}
			b := byte(tok.base + v)
			z.put(b)
			out = append(out, b)
			continue
		}
		dist := int(tok.base + v)
		if dist == 0 { // unencoded run: 15-bit count, then whole bytes from the next byte boundary
			cnt, ok := r.bits(15)
			if !ok {
				return nil, errZGFX
			}
			raw, ok := r.bytes(int(cnt))
			if !ok || len(out)-start+len(raw) > zgfxSegmentMax {
				return nil, errZGFX
			}
			for _, b := range raw {
				z.put(b)
			}
			out = append(out, raw...)
			continue
		}
		cnt := 3
		if b, ok := r.bits(1); !ok {
			return nil, errZGFX
		} else if b == 1 {
			cnt, extra := 4, uint8(2)
			for {
				b, ok := r.bits(1)
				if !ok || extra > 15 {
					return nil, errZGFX
				}
				if b == 0 {
					break
				}
				cnt, extra = cnt*2, extra+1
			}
			e, ok := r.bits(extra)
			if !ok {
				return nil, errZGFX
			}
			cnt += int(e)
			if out, ok = z.copyMatch(out, start, dist, cnt); !ok {
				return nil, errZGFX
			}
			continue
		}
		var ok2 bool
		if out, ok2 = z.copyMatch(out, start, dist, cnt); !ok2 {
			return nil, errZGFX
		}
	}
	return out, nil
}

// copyMatch copies cnt bytes from dist back in the history (overlapping copies repeat).
func (z *ZGFX) copyMatch(out []byte, start, dist, cnt int) ([]byte, bool) {
	if len(out)-start+cnt > zgfxSegmentMax || dist > len(z.hist) || (!z.full && dist > z.pos) {
		return out, false
	}
	src := z.pos - dist
	if src < 0 {
		src += len(z.hist)
	}
	for i := 0; i < cnt; i++ {
		b := z.hist[src]
		if src++; src == len(z.hist) {
			src = 0
		}
		z.put(b)
		out = append(out, b)
	}
	return out, true
}

// bitReader reads MSB-first bits; left counts the valid bits that remain.
type bitReader struct {
	data []byte
	pos  int // bit position
	left int
}

func (r *bitReader) bits(n uint8) (uint32, bool) {
	if int(n) > r.left {
		return 0, false
	}
	var v uint32
	for i := uint8(0); i < n; i++ {
		v = v<<1 | uint32(r.data[r.pos>>3]>>(7-r.pos&7)&1)
		r.pos++
	}
	r.left -= int(n)
	return v, true
}

// bytes skips to the next byte boundary and returns n whole bytes.
func (r *bitReader) bytes(n int) ([]byte, bool) {
	skip := (8 - r.pos&7) & 7
	if skip > r.left {
		return nil, false
	}
	r.pos += skip
	r.left -= skip
	i := r.pos >> 3
	if n*8 > r.left || i+n > len(r.data) {
		return nil, false
	}
	r.pos += 8 * n
	r.left -= 8 * n
	return r.data[i : i+n], true
}
