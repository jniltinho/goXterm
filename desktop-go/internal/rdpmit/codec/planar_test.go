package codec

import "testing"

// TestPlanarDeltaWrapsAndAlphaIsOpaque: a 1×2 RLE plane whose second scanline adds +10 to 250 must
// wrap to 4 (as FreeRDP does), and the decoded pixels are opaque even with an alpha plane of 0.
func TestPlanarDeltaWrapsAndAlphaIsOpaque(t *testing.T) {
	plane := []byte{0x10, 250, 0x10, 20} // raw 250; then delta byte 20 = +10
	src := []byte{PlanarFlagRLE}
	src = append(src, 0x10, 0, 0x10, 0) // alpha plane: 0, +0
	for i := 0; i < 3; i++ {            // R, G, B
		src = append(src, plane...)
	}
	rgba := DecompressPlanar(src, 1, 2)
	if rgba == nil {
		t.Fatal("decode failed")
	}
	// bottom-up: the second scanline (4) is the top row
	if got := rgba[0:4]; got[0] != 4 || got[1] != 4 || got[2] != 4 || got[3] != 255 {
		t.Fatalf("top pixel = %v, want [4 4 4 255]", got)
	}
	if got := rgba[4:8]; got[0] != 250 || got[3] != 255 {
		t.Fatalf("bottom pixel = %v, want [250 … 255]", got)
	}
}
