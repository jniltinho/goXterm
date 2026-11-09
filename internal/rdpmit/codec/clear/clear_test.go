package clear

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

// MS-RDPEGFX 4.1.1.1 examples 2-4 (also FreeRDP's TestFreeRDPCodecClear, which checks that they
// decode).
var (
	example2 = []byte("\x00\x0d\x00\x00\x00\x00\x00\x00\x00\x00\x82\x00\x00\x00\x00\x00" +
		"\x00\x00\x4e\x00\x11\x00\x75\x00\x00\x00\x02\x0e\xff\xff\xff\x00" +
		"\x00\x00\xdb\xff\xff\x00\x3a\x90\xff\xb6\x66\x66\xb6\xff\xb6\x66" +
		"\x00\x90\xdb\xff\x00\x00\x3a\xdb\x90\x3a\x3a\x90\xdb\x66\x00\x00" +
		"\xff\xff\xb6\x64\x64\x64\x11\x04\x11\x4c\x11\x4c\x11\x4c\x11\x4c" +
		"\x11\x4c\x00\x47\x13\x00\x01\x01\x04\x00\x01\x00\x00\x47\x16\x00" +
		"\x11\x02\x00\x47\x29\x00\x11\x01\x00\x49\x0a\x00\x01\x00\x04\x00" +
		"\x01\x00\x00\x4a\x0a\x00\x09\x00\x01\x00\x00\x47\x05\x00\x01\x01" +
		"\x1c\x00\x01\x00\x11\x4c\x11\x4c\x11\x4c\x00\x47\x0d\x4d\x00\x4d")
	example3 = []byte("\x00\xdf\x0e\x00\x00\x00\x8b\x00\x00\x00\x00\x00\x00\x00\xfe\xfe" +
		"\xfe\xff\x80\x05\xff\xff\xff\x40\xfe\xfe\xfe\x40\x00\x00\x3f\x00" +
		"\x03\x00\x0b\x00\xfe\xfe\xfe\xc5\xd0\xc6\xd0\xc7\xd0\x68\xd4\x69" +
		"\xd4\x6a\xd4\x6b\xd4\x6c\xd4\x6d\xd4\x1a\xd4\x1a\xd4\xa6\xd0\x6e" +
		"\xd4\x6f\xd4\x70\xd4\x71\xd4\x72\xd4\x73\xd4\x74\xd4\x21\xd4\x22" +
		"\xd4\x23\xd4\x24\xd4\x25\xd4\xd9\xd0\xda\xd0\xdb\xd0\xc5\xd0\xc5" +
		"\xd0\xdc\xd0\xc2\xd0\x21\xd4\x22\xd4\x23\xd4\x24\xd4\x25\xd4\xc9" +
		"\xd0\xca\xd0\x5a\xd4\x2b\xd1\x28\xd1\x2c\xd1\x75\xd4\x27\xd4\x28" +
		"\xd4\x29\xd4\x2a\xd4\x1a\xd4\x1a\xd4\x1a\xd4\xb7\xd0\xb8\xd0\xb9" +
		"\xd0\xba\xd0\xbb\xd0\xbc\xd0\xbd\xd0\xbe\xd0\xbf\xd0\xc0\xd0\xc1" +
		"\xd0\xc2\xd0\xc3\xd0\xc4\xd0")
	example4 = []byte("\x01\x0b\x78\x00\x00\x00\x00\x00\x46\x00\x00\x00\x00\x00\x00\x00" +
		"\x00\x00\x06\x00\x00\x00\x0e\x00\x00\x00\x00\x00\x0f\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xb6\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xb6\x66\xff\xff\xff\xff\xff\xff\xff\xb6\x66\xdb\x90\x3a\xff\xff" +
		"\xb6\xff\xff\xff\xff\xff\xff\xff\xff\xff\x46\x91\x47\x91\x48\x91" +
		"\x49\x91\x4a\x91\x1b\x91")
)

func TestSpecExamplesDecode(t *testing.T) {
	for _, ex := range []struct {
		src  []byte
		w, h int
	}{{example2, 78, 17}, {example3, 64, 24}, {example4, 7, 15}} {
		var d Decoder
		pix := make([]byte, ex.w*ex.h*4)
		if err := d.Decode(ex.src, pix, ex.w*4, 0, 0, ex.w, ex.h); err != nil {
			t.Errorf("%d×%d: %v", ex.w, ex.h, err)
		}
	}
	// example 4 stored glyph 0x78: a glyph hit with the same size draws it again
	var d Decoder
	pix := make([]byte, 7*15*4)
	if err := d.Decode(example4, pix, 7*4, 0, 0, 7, 15); err != nil {
		t.Fatal(err)
	}
	again := make([]byte, len(pix))
	if err := d.Decode([]byte{0x03, 0x0c, 0x78, 0x00}, again, 7*4, 0, 0, 7, 15); err != nil || !bytes.Equal(again, pix) {
		t.Fatalf("glyph hit: %v", err)
	}
}

func layers(flags, seq byte, residual, bands, sub []byte) []byte {
	b := []byte{flags, seq}
	b = binary.LittleEndian.AppendUint32(b, uint32(len(residual)))
	b = binary.LittleEndian.AppendUint32(b, uint32(len(bands)))
	b = binary.LittleEndian.AppendUint32(b, uint32(len(sub)))
	return append(append(append(b, residual...), bands...), sub...)
}

func px(pix []byte, stride, x, y int) [4]byte {
	o := y*stride + x*4
	return [4]byte(pix[o : o+4])
}

func TestLayers(t *testing.T) {
	var d Decoder
	const w, h, stride = 4, 3, 6 * 4 // drawn at (1,1) in a 6×5 area
	pix := make([]byte, stride*5)
	// residual: 11 blue pixels, then one red one (B, G, R, run)
	residual := []byte{255, 0, 0, 11, 0, 0, 255, 1}
	// band x 1..2, y 0..2, background green: a short-bar miss (yOn 1, yOff 2, one white pixel), then
	// a hit of the full vBar just stored (slot 0)
	bands := []byte{1, 0, 2, 0, 0, 0, 2, 0, 0, 255, 0}
	bands = binary.LittleEndian.AppendUint16(bands, 2<<8|1)
	bands = append(bands, 255, 255, 255)
	bands = binary.LittleEndian.AppendUint16(bands, 0x8000)
	// subcodecs: uncompressed 1×1 gray at (3,0); RLEX 2×1 at (0,2): palette black, white; one
	// segment with run 0 and a suite 0..1
	sub := []byte{3, 0, 0, 0, 1, 0, 1, 0, 3, 0, 0, 0, 0, 0x80, 0x80, 0x80}
	rlex := []byte{2, 0, 0, 0, 255, 255, 255, 1<<1 | 1, 0}
	sub = append(sub, 0, 0, 2, 0, 2, 0, 1, 0)
	sub = binary.LittleEndian.AppendUint32(sub, uint32(len(rlex)))
	sub = append(append(sub, 2), rlex...)
	if err := d.Decode(layers(0, 1, residual, bands, sub), pix, stride, 1, 1, w, h); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		x, y int
		want [4]byte
	}{
		{0, 0, [4]byte{}}, // outside the bitmap: untouched
		{1, 1, [4]byte{0, 0, 255, 255}},
		{2, 1, [4]byte{0, 255, 0, 255}},     // band background above yOn
		{2, 2, [4]byte{255, 255, 255, 255}}, // short bar
		{3, 2, [4]byte{255, 255, 255, 255}}, // vBar cache hit
		{3, 3, [4]byte{0, 255, 0, 255}},
		{4, 1, [4]byte{0x80, 0x80, 0x80, 255}}, // uncompressed subcodec
		{1, 3, [4]byte{0, 0, 0, 255}},          // RLEX suite
		{2, 3, [4]byte{255, 255, 255, 255}},
		{4, 3, [4]byte{255, 0, 0, 255}}, // last residual pixel
	} {
		if got := px(pix, stride, c.x, c.y); got != c.want {
			t.Errorf("pixel %d,%d = %v, want %v", c.x, c.y, got, c.want)
		}
	}
	// the next sequence number is 2; a gap is an error
	if err := d.Decode(layers(0, 3, residual, nil, nil), pix, stride, 1, 1, w, h); err == nil {
		t.Error("sequence gap accepted")
	}
	d.ResetSequence()
	if err := d.Decode(layers(0, 9, residual, nil, nil), pix, stride, 1, 1, w, h); err != nil {
		t.Errorf("after reset: %v", err)
	}
}

func TestMalformed(t *testing.T) {
	pix := make([]byte, 16*16*4)
	for name, src := range map[string][]byte{
		"short":          {0},
		"hit no index":   {0x02, 0},
		"glyph range":    {0x01, 0, 0xA0, 0x0F},
		"empty glyph":    {0x03, 0, 0x01, 0x00},
		"layers overrun": layers(0, 0, []byte{1, 2, 3, 200}, nil, nil)[:16],
		"residual short": layers(0, 0, []byte{1, 2, 3, 4}, nil, nil),
		"residual long":  layers(0, 0, []byte{1, 2, 3, 0xFF, 0x00, 0x10}, nil, nil),
		"tall band":      layers(0, 0, nil, []byte{0, 0, 0, 0, 0, 0, 60, 0, 0, 0, 0}, nil),
		"subcodec 9":     layers(0, 0, nil, nil, []byte{0, 0, 0, 0, 1, 0, 1, 0, 0, 0, 0, 0, 9}),
		"subcodec out":   layers(0, 0, nil, nil, []byte{15, 0, 0, 0, 2, 0, 1, 0, 6, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1}),
		"rlex palette":   layers(0, 0, nil, nil, []byte{0, 0, 0, 0, 1, 0, 1, 0, 1, 0, 0, 0, 2, 0}),
	} {
		var d Decoder
		if err := d.Decode(src, pix, 16*4, 0, 0, 16, 16); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	rng := rand.New(rand.NewSource(4))
	var d Decoder
	for i := 0; i < 100000; i++ {
		var src []byte
		switch rng.Intn(3) {
		case 0:
			src = make([]byte, rng.Intn(48))
			rng.Read(src)
		default: // well-formed header, random layer contents
			l := [3][]byte{}
			k := rng.Intn(3)
			l[k] = make([]byte, rng.Intn(40))
			rng.Read(l[k])
			src = layers(byte(rng.Intn(8)), 0, l[0], l[1], l[2])
		}
		d.ResetSequence()
		w, h := 1+rng.Intn(16), 1+rng.Intn(16)
		_ = d.Decode(src, pix, 16*4, 16-w, 16-h, w, h)
	}
}
