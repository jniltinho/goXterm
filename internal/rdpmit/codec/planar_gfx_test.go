package codec

import (
	"bytes"
	"testing"
)

func TestDecodePlanarGFX(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  []byte
		w, h int
		want []byte
	}{
		{"raw RGB with alpha, top-down", []byte{0x00,
			1, 2, 3, 4, // A
			10, 11, 12, 13, // R
			20, 21, 22, 23, // G
			30, 31, 32, 33}, 2, 2,
			[]byte{10, 20, 30, 1, 11, 21, 31, 2, 12, 22, 32, 3, 13, 23, 33, 4}},
		{"RLE RGB without alpha", []byte{0x30, 0x20, 10, 20, 0x20, 30, 40, 0x20, 50, 60}, 2, 1,
			[]byte{10, 30, 50, 255, 20, 40, 60, 255}},
		{"YCoCg, chroma subsampled", []byte{0x29, 100, 100, 100, 100, 10, 0}, 2, 2,
			bytes.Repeat([]byte{110, 100, 90, 255}, 4)},
		{"YCoCg color loss 2", []byte{0x22, 100, 5, 0}, 1, 1, // Co 5<<1 = 10
			[]byte{110, 100, 90, 255}},
	} {
		got, err := DecodePlanarGFX(tc.src, tc.w, tc.h)
		if err != nil || !bytes.Equal(got, tc.want) {
			t.Errorf("%s: %v %v, want %v", tc.name, got, err, tc.want)
		}
	}
	for _, bad := range [][]byte{nil, {0x08}, {0x20, 1, 2}, {0x30, 0xFF}} {
		if _, err := DecodePlanarGFX(bad, 2, 2); err == nil {
			t.Errorf("% x accepted", bad)
		}
	}
}
