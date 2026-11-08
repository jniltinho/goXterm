package codec

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestNSCodecRLE: a value followed by itself is a run (count+2, or 0xFF + 32-bit length); the last
// 4 bytes are raw.
func TestNSCodecRLE(t *testing.T) {
	in := []byte{7, 7, 3, 8, 9, 9, 0xFF, 6, 0, 0, 0, 1, 2, 3, 4} // 7×5, 8, 9×6, raw 1 2 3 4
	want := append(append(bytes.Repeat([]byte{7}, 5), 8), append(bytes.Repeat([]byte{9}, 6), 1, 2, 3, 4)...)
	if got := nscRLEDecode(in, len(want)); !bytes.Equal(got, want) {
		t.Fatalf("got % x\nwant % x", got, want)
	}
}

// TestNSCodecDecode: a 1×2 stream with raw planes (no subsampling, colour loss 1) decodes YCoCg
// with signed Co/Cg, an absent alpha plane is opaque, and rows are flipped (bottom-up).
func TestNSCodecDecode(t *testing.T) {
	luma, co, cg := []byte{100, 200}, []byte{0, 0xF6}, []byte{0, 10} // row1: Co=-10, Cg=+10
	hdr := make([]byte, 20)
	for i, n := range []int{2, 2, 2, 0} {
		binary.LittleEndian.PutUint32(hdr[4*i:], uint32(n))
	}
	hdr[16] = 1 // colour loss level 1 (no shift)
	data := append(append(append(hdr, luma...), co...), cg...)
	rgba := DecodeNSCodecToRGBA(data, 1, 2)
	// stream row 1 (Y 200, Co -10, Cg 10) is the top row: R=200-10-10, G=210, B=200+10-10
	if want := []byte{180, 210, 200, 255, 100, 100, 100, 255}; !bytes.Equal(rgba, want) {
		t.Fatalf("rgba = %v, want %v", rgba, want)
	}
}
