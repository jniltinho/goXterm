// rustxterm addition (not in upstream go-rdp, which only has a ClearCodec encoder subset): the
// ClearCodec decoder of the RDP Graphics Pipeline (MS-RDPEGFX 2.2.4.1 and 3.3.8.1), written from the
// specification and checked against FreeRDP's behavior (libfreerdp/codec/clear.c, Apache-2.0).

// Package clear decodes ClearCodec bitmaps: the residual layer, bands of vertical bars with their
// caches, the subcodec layer (uncompressed, NSCodec, RLEX) and the glyph cache.
package clear

import (
	"encoding/binary"
	"errors"
	"fmt"

	"rustxterm-go/internal/rdpmit/codec"
)

const (
	flagGlyphIndex = 0x01 // CLEARCODEC_FLAG_GLYPH_INDEX
	flagGlyphHit   = 0x02 // CLEARCODEC_FLAG_GLYPH_HIT
	flagCacheReset = 0x04 // CLEARCODEC_FLAG_CACHE_RESET

	vBarSlots      = 32768
	shortVBarSlots = 16384
	glyphSlots     = 4000
	maxGlyphPixels = 1 << 20  // per glyph (FreeRDP's bound; the spec uses glyphs up to 1,024 pixels)
	maxGlyphBytes  = 64 << 20 // all glyphs together
	maxVBarHeight  = 52
)

var errData = errors.New("clearcodec: malformed data")

// Decoder holds the ClearCodec state of one graphics channel; its caches persist across
// ResetGraphics.
type Decoder struct {
	seq     uint8
	vBars   [vBarSlots][]byte // RGBA columns
	vCursor int
	short   [shortVBarSlots][]byte
	sCursor int
	glyphs  [glyphSlots][]byte // RGBA, w*h pixels in decode order
	glyphB  int
}

// ResetSequence restarts sequence-number checking (on ResetGraphics); the caches are kept.
func (d *Decoder) ResetSequence() { d.seq = 0 }

// target is the destination area: RGBA pixels with stride, the bitmap drawn at x, y, w×h.
type target struct {
	pix          []byte
	stride, x, y int
	w, h         int
}

func (t *target) set(x, y int, r, g, b byte) {
	o := (t.y+y)*t.stride + (t.x+x)*4
	t.pix[o], t.pix[o+1], t.pix[o+2], t.pix[o+3] = r, g, b, 0xFF
}

// Decode draws one ClearCodec bitmap of w×h at (x, y) of pix (RGBA, stride bytes per row). The
// caller guarantees the rectangle lies inside pix.
func (d *Decoder) Decode(src, pix []byte, stride, x, y, w, h int) error {
	if len(src) < 2 || w <= 0 || h <= 0 {
		return errData
	}
	t := &target{pix: pix, stride: stride, x: x, y: y, w: w, h: h}
	flags, seq := src[0], src[1]
	src = src[2:]
	// the first sequence number is taken as is (FreeRDP); then each one must follow the last
	if d.seq == 0 && seq != 0 {
		d.seq = seq
	}
	if seq != d.seq {
		return fmt.Errorf("clearcodec: sequence number %d, expected %d", seq, d.seq)
	}
	d.seq = seq + 1
	if flags&flagCacheReset != 0 {
		d.vCursor, d.sCursor = 0, 0
	}
	if flags&flagGlyphHit != 0 && flags&flagGlyphIndex == 0 {
		return errData
	}
	glyph := -1
	if flags&flagGlyphIndex != 0 {
		if w*h > maxGlyphPixels || len(src) < 2 {
			return errData
		}
		glyph = int(binary.LittleEndian.Uint16(src))
		src = src[2:]
		if glyph >= glyphSlots {
			return errData
		}
		if flags&flagGlyphHit != 0 {
			g := d.glyphs[glyph]
			if len(g) < w*h*4 {
				return fmt.Errorf("clearcodec: glyph %d missing or smaller than %d×%d", glyph, w, h)
			}
			for row := 0; row < h; row++ {
				copy(pix[(y+row)*stride+x*4:], g[row*w*4:(row+1)*w*4])
			}
			if len(src) < 12 {
				return nil
			}
			glyph = -1 // a hit is not stored again
		}
	}
	if len(src) < 12 {
		return errData
	}
	residual := binary.LittleEndian.Uint32(src)
	bands := binary.LittleEndian.Uint32(src[4:])
	subcodecs := binary.LittleEndian.Uint32(src[8:])
	src = src[12:]
	if uint64(residual)+uint64(bands)+uint64(subcodecs) > uint64(len(src)) {
		return errData
	}
	if residual > 0 {
		if err := t.residual(src[:residual]); err != nil {
			return err
		}
	}
	src = src[residual:]
	if bands > 0 {
		if err := d.bands(t, src[:bands]); err != nil {
			return err
		}
	}
	src = src[bands:]
	if subcodecs > 0 {
		if err := t.subcodecs(src[:subcodecs]); err != nil {
			return err
		}
	}
	if glyph >= 0 {
		d.glyphB += w*h*4 - len(d.glyphs[glyph])
		if d.glyphB > maxGlyphBytes {
			return fmt.Errorf("clearcodec: glyph cache over %d bytes", maxGlyphBytes)
		}
		g := make([]byte, w*h*4)
		for row := 0; row < h; row++ {
			copy(g[row*w*4:], pix[(y+row)*stride+x*4:(y+row)*stride+(x+w)*4])
		}
		d.glyphs[glyph] = g
	}
	return nil
}

// runLength reads a run length factor: one byte, or 0xFF then a u16, or 0xFFFF then a u32.
func runLength(p []byte) (int, []byte, bool) {
	if len(p) < 1 {
		return 0, nil, false
	}
	n := int(p[0])
	p = p[1:]
	if n < 0xFF {
		return n, p, true
	}
	if len(p) < 2 {
		return 0, nil, false
	}
	n = int(binary.LittleEndian.Uint16(p))
	p = p[2:]
	if n < 0xFFFF {
		return n, p, true
	}
	if len(p) < 4 {
		return 0, nil, false
	}
	return int(binary.LittleEndian.Uint32(p)), p[4:], true
}

// residual is the first layer: run-length BGR covering the whole bitmap.
func (t *target) residual(p []byte) error {
	total, i := t.w*t.h, 0
	for len(p) > 0 {
		if len(p) < 4 {
			return errData
		}
		b, g, r := p[0], p[1], p[2]
		n, rest, ok := runLength(p[3:])
		if !ok || n > total-i {
			return errData
		}
		p = rest
		for ; n > 0; n, i = n-1, i+1 {
			t.set(i%t.w, i/t.w, r, g, b)
		}
	}
	if i != total {
		return errData
	}
	return nil
}

// bands draws the second layer: bands of vertical bars, through the vBar and shortVBar caches.
func (d *Decoder) bands(t *target, p []byte) error {
	for len(p) > 0 {
		if len(p) < 11 {
			return errData
		}
		x0, x1 := int(binary.LittleEndian.Uint16(p)), int(binary.LittleEndian.Uint16(p[2:]))
		y0, y1 := int(binary.LittleEndian.Uint16(p[4:])), int(binary.LittleEndian.Uint16(p[6:]))
		bkg := [4]byte{p[10], p[9], p[8], 0xFF}
		p = p[11:]
		if x1 < x0 || y1 < y0 || y1-y0+1 > maxVBarHeight {
			return errData
		}
		height := y1 - y0 + 1
		for i := 0; i <= x1-x0; i++ {
			if len(p) < 2 {
				return errData
			}
			hdr := binary.LittleEndian.Uint16(p)
			p = p[2:]
			var bar []byte
			switch {
			case hdr&0x8000 != 0: // VBAR_CACHE_HIT
				bar = d.vBars[hdr&0x7FFF]
			case hdr&0xC000 == 0x4000: // SHORT_VBAR_CACHE_HIT
				if len(p) < 1 {
					return errData
				}
				bar = d.store(height, int(p[0]), d.short[hdr&0x3FFF], bkg)
				p = p[1:]
			default: // SHORT_VBAR_CACHE_MISS
				on, off := int(hdr&0xFF), int(hdr>>8&0x3F)
				if off < on || off-on > maxVBarHeight || len(p) < 3*(off-on) {
					return errData
				}
				short := make([]byte, 4*(off-on))
				for k := range off - on {
					short[4*k], short[4*k+1], short[4*k+2], short[4*k+3] = p[3*k+2], p[3*k+1], p[3*k], 0xFF
				}
				p = p[3*(off-on):]
				d.short[d.sCursor] = short
				d.sCursor = (d.sCursor + 1) % shortVBarSlots
				bar = d.store(height, on, short, bkg)
			}
			// draw the column, clipped to the bitmap; a cache entry from a reset or of another
			// height is padded with black (FreeRDP resizes it the same way)
			x := x0 + i
			if x >= t.w {
				continue
			}
			for k := 0; k < height && y0+k < t.h; k++ {
				var c [4]byte
				if 4*k+3 < len(bar) {
					c = [4]byte(bar[4*k : 4*k+4])
				}
				t.set(x, y0+k, c[0], c[1], c[2])
			}
		}
	}
	return nil
}

// store builds a full vBar (background above yOn, the short bar, background below) and puts it in
// the vBar cache.
func (d *Decoder) store(height, on int, short []byte, bkg [4]byte) []byte {
	bar := make([]byte, 4*height)
	for k := range height {
		c := bkg[:]
		if s := 4 * (k - on); k >= on && s+4 <= len(short) {
			c = short[s : s+4]
		}
		copy(bar[4*k:], c)
	}
	d.vBars[d.vCursor] = bar
	d.vCursor = (d.vCursor + 1) % vBarSlots
	return bar
}

// subcodecs draws the third layer: rectangles in uncompressed BGR, NSCodec or RLEX.
func (t *target) subcodecs(p []byte) error {
	for len(p) > 0 {
		if len(p) < 13 {
			return errData
		}
		x, y := int(binary.LittleEndian.Uint16(p)), int(binary.LittleEndian.Uint16(p[2:]))
		w, h := int(binary.LittleEndian.Uint16(p[4:])), int(binary.LittleEndian.Uint16(p[6:]))
		n := binary.LittleEndian.Uint32(p[8:])
		id := p[12]
		p = p[13:]
		if uint64(n) > uint64(len(p)) || x+w > t.w || y+h > t.h {
			return errData
		}
		data := p[:n]
		p = p[n:]
		if w == 0 || h == 0 {
			continue
		}
		switch id {
		case 0: // uncompressed BGR
			if len(data) != w*h*3 {
				return errData
			}
			for i := range w * h {
				t.set(x+i%w, y+i/w, data[3*i+2], data[3*i+1], data[3*i])
			}
		case 1: // NSCodec
			rgba := codec.DecodeNSCodec(data, w, h)
			if rgba == nil {
				return errors.New("clearcodec: bad NSCodec subcodec data")
			}
			for i := range w * h {
				t.set(x+i%w, y+i/w, rgba[4*i], rgba[4*i+1], rgba[4*i+2])
			}
		case 2:
			if err := t.rlex(data, x, y, w, h); err != nil {
				return err
			}
		default:
			return fmt.Errorf("clearcodec: subcodec %d", id)
		}
	}
	return nil
}

// rlex is CLEARCODEC_SUBCODEC_RLEX: a palette, then runs of one color each followed by a suite of
// consecutive palette entries.
func (t *target) rlex(p []byte, x, y, w, h int) error {
	if len(p) < 1 {
		return errData
	}
	count := int(p[0])
	if count < 1 || count > 127 || len(p) < 1+3*count {
		return errData
	}
	var pal [128][3]byte
	for i := range count {
		pal[i] = [3]byte{p[1+3*i+2], p[1+3*i+1], p[1+3*i]}
	}
	p = p[1+3*count:]
	bits := 1
	for v := count - 1; v > 1; v >>= 1 { // floor(log2(count-1)) + 1
		bits++
	}
	total, i := w*h, 0
	put := func(c [3]byte) {
		t.set(x+i%w, y+i/w, c[0], c[1], c[2])
		i++
	}
	for len(p) > 0 {
		if len(p) < 2 {
			return errData
		}
		v := int(p[0])
		depth := v >> bits & (0xFF >> bits)
		stop := v & (1<<bits - 1)
		start := stop - depth
		n, rest, ok := runLength(p[1:])
		if !ok || start < 0 || stop >= count || n+depth+1 > total-i {
			return errData
		}
		p = rest
		for range n {
			put(pal[start])
		}
		for k := start; k <= stop; k++ {
			put(pal[k])
		}
	}
	if i != total {
		return errData
	}
	return nil
}
