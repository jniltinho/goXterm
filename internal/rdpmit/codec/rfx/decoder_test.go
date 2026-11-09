package rfx

import (
	"image"
	"math/rand"
	"os"
	"testing"
)

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestSpecMessage decodes the MS-RDPRFX 4.2 example (header and data messages, one 64×64 tile in
// RLGR3) and compares it with the specification's reference image (4.2.4.4), ±1 per channel as
// FreeRDP's TestFreeRDPCodecRemoteFX does.
func TestSpecMessage(t *testing.T) {
	var d Decoder
	dst := make([]byte, 64*64*4)
	if _, err := d.Decode(readFile(t, "msrdprfx-header.bin"), dst, 64*4, 64, 64, 0, 0); err != nil {
		t.Fatal(err)
	}
	changed, err := d.Decode(readFile(t, "msrdprfx-data.bin"), dst, 64*4, 64, 64, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0] != image.Rect(0, 0, 64, 64) {
		t.Fatalf("changed %v", changed)
	}
	ref := readFile(t, "msrdprfx-image.rgb")
	bad := 0
	for i := 0; i < 64*64; i++ {
		for c := 0; c < 3; c++ {
			if diff := int(dst[4*i+c]) - int(ref[3*i+c]); diff > 1 || diff < -1 {
				bad++
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d channel values differ from the reference by more than 1", bad)
	}
}

// block builds an RFX block; codec channel blocks get codecId 1 and the channel id.
func block(typ uint16, channel int, body ...byte) []byte {
	b := []byte{byte(typ), byte(typ >> 8), 0, 0, 0, 0}
	if channel >= 0 {
		b = append(b, 1, byte(channel))
	}
	b = append(b, body...)
	b[2], b[3] = byte(len(b)), byte(len(b)>>8)
	return b
}

func headers(entropy byte) []byte {
	var m []byte
	m = append(m, block(WBT_SYNC, -1, 0xCA, 0xAC, 0xCC, 0xCA, 0x00, 0x01)...)
	m = append(m, block(WBT_CODEC_VERSIONS, -1, 1, 1, 0x00, 0x01)...)
	m = append(m, block(WBT_CHANNELS, -1, 1, 0, 128, 0, 128, 0)...)
	props := uint16(entropy) << 9
	return append(m, block(WBT_CONTEXT, 0xFF, 0, 0x40, 0, byte(props), byte(props>>8))...)
}

// grayFrame is one frame with a region rect and one empty tile (all coefficients 0: gray 128).
func grayFrame(rx, ry, rw, rh, tx, ty int, quantIdx byte) []byte {
	m := block(WBT_FRAME_BEGIN, 0, 0, 0, 0, 0, 1, 0)
	m = append(m, block(WBT_REGION, 0, 1, 1, 0, byte(rx), 0, byte(ry), 0, byte(rw), 0, byte(rh), 0, 0xC1, 0xCA, 1, 0)...)
	tile := []byte{0xC3, 0xCA, 19, 0, 0, 0, quantIdx, 0, 0, byte(tx), 0, byte(ty), 0, 0, 0, 0, 0, 0, 0}
	ts := []byte{0xC2, 0xCA, 0, 0, 0, 0, 1, 0x40, 1, 0, byte(len(tile)), 0, 0, 0, 0x66, 0x66, 0x77, 0x88, 0x98}
	m = append(m, block(WBT_EXTENSION, 0, append(ts, tile...)...)...)
	return append(m, block(WBT_FRAME_END, 0)...)
}

func TestRegionClipsTiles(t *testing.T) {
	var d Decoder
	const w, h = 160, 100
	dst := make([]byte, w*h*4)
	// message origin (16, 8); tile (1, 0) covers 80..144 × 8..72; region 70..90 × 18..28
	changed, err := d.Decode(append(headers(CLW_ENTROPY_RLGR1), grayFrame(54, 10, 20, 10, 1, 0, 0)...), dst, w*4, w, h, 16, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0] != image.Rect(80, 18, 90, 28) {
		t.Fatalf("changed %v", changed)
	}
	at := func(x, y int) byte { return dst[(y*w+x)*4] }
	if at(80, 18) != 128 || at(89, 27) != 128 || at(79, 18) != 0 || at(90, 18) != 0 || at(80, 28) != 0 {
		t.Fatalf("clipping: %d %d %d %d %d", at(80, 18), at(89, 27), at(79, 18), at(90, 18), at(80, 28))
	}
	// the headers persist: a later message may carry data blocks only
	if _, err := d.Decode(grayFrame(0, 0, 8, 8, 0, 0, 0), dst, w*4, w, h, 0, 0); err != nil {
		t.Fatal(err)
	}
	// a tile clipped by the surface edge is drawn partly
	changed, err = d.Decode(grayFrame(128, 64, 64, 64, 2, 1, 0), dst, w*4, w, h, 0, 0)
	if err != nil || len(changed) != 1 || changed[0] != image.Rect(128, 64, 160, 100) {
		t.Fatalf("edge: %v %v", changed, err)
	}
}

func TestMalformedMessages(t *testing.T) {
	dst := make([]byte, 64*64*4)
	for name, m := range map[string][]byte{
		"data before headers": grayFrame(0, 0, 8, 8, 0, 0, 0),
		"quant index":         append(headers(CLW_ENTROPY_RLGR3), grayFrame(0, 0, 8, 8, 0, 0, 1)...),
		"bad entropy":         headers(2),
		"out of order":        append(headers(CLW_ENTROPY_RLGR3), block(WBT_FRAME_END, 0)...),
		"block length":        {0xC0, 0xCC, 0xFF, 0, 0, 0, 1},
		"wrong channel":       block(WBT_CONTEXT, 0, 0, 0x40, 0, 0, 0),
	} {
		var d Decoder
		if _, err := d.Decode(m, dst, 64*4, 64, 64, 0, 0); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	rng := rand.New(rand.NewSource(5))
	good := append(headers(CLW_ENTROPY_RLGR3), grayFrame(0, 0, 64, 64, 0, 0, 0)...)
	for i := 0; i < 20000; i++ {
		m := append([]byte(nil), good...)
		for k := rng.Intn(4); k >= 0; k-- {
			m[rng.Intn(len(m))] = byte(rng.Intn(256))
		}
		m = m[:rng.Intn(len(m)+1)]
		var d Decoder
		_, _ = d.Decode(m, dst, 64*4, 64, 64, rng.Intn(80), rng.Intn(80))
	}
}
