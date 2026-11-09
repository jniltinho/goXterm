package rdpgfx

import (
	"math/rand"
	"testing"
)

// MS-RDPEGFX 4.1.2 / 4.1.3 examples (also FreeRDP's TestFreeRDPCodecZGfx).
const fox = "The quick brown fox jumps over the lazy dog"

var foxSingle = []byte("\xE0\x04\x54\x68\x65\x20\x71\x75\x69\x63\x6B\x20\x62\x72\x6F\x77" +
	"\x6E\x20\x66\x6F\x78\x20\x6A\x75\x6D\x70\x73\x20\x6F\x76\x65\x72" +
	"\x20\x74\x68\x65\x20\x6C\x61\x7A\x79\x20\x64\x6F\x67")

var foxMultipart = []byte("\xE1\x03\x00\x2B\x00\x00\x00\x11\x00\x00\x00\x04\x54\x68\x65\x20" +
	"\x71\x75\x69\x63\x6B\x20\x62\x72\x6F\x77\x6E\x20\x0E\x00\x00\x00" +
	"\x04\x66\x6F\x78\x20\x6A\x75\x6D\x70\x73\x20\x6F\x76\x65\x10\x00" +
	"\x00\x00\x24\x39\x08\x0E\x91\xF8\xD8\x61\x3D\x1E\x44\x06\x43\x79" +
	"\x9C\x02")

func TestZGFXVectors(t *testing.T) {
	var z ZGFX
	got, err := z.Decompress(foxSingle)
	if err != nil || string(got) != fox {
		t.Fatalf("single: %q %v", got, err)
	}
	var z2 ZGFX
	got, err = z2.Decompress(foxMultipart)
	if err != nil || string(got) != fox {
		t.Fatalf("multipart: %q %v", got, err)
	}
	// the history persists: the same compressed segment decodes again against it
	got, err = z2.Decompress(foxMultipart)
	if err != nil || string(got) != fox {
		t.Fatalf("multipart again: %q %v", got, err)
	}
}

func TestZGFXLimits(t *testing.T) {
	var z ZGFX
	for _, in := range [][]byte{
		nil,
		{0xE2, 0x04},
		{0xE1, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F},                                    // 2 GiB announced
		{0xE1, 0x01, 0x00, 0x05, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x04, 'a'}, // shorter than announced
		{0xE0, 0x24, 0xFF},       // compressed, padding beyond the data
		{0xE0, 0x24, 0x80, 0x00}, // a match before any history
	} {
		if _, err := z.Decompress(in); err == nil {
			t.Errorf("% x accepted", in)
		}
	}
}

func TestZGFXMalformedNeverPanics(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	var z ZGFX
	for i := 0; i < 50000; i++ {
		b := make([]byte, 2+rng.Intn(40))
		rng.Read(b)
		b[0] = 0xE0 + byte(rng.Intn(2))
		if rng.Intn(2) == 0 {
			b[1] |= zgfxCompressed
		}
		_, _ = z.Decompress(b)
	}
}
