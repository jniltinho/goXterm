package rdpconn

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"rustxterm-go/internal/rdpmit/rdpgfx"
)

func TestGFXFrameEncodesBands(t *testing.T) {
	var got []Rect
	s := &Session{onFrame: func(r []Rect, frames []uint32) { got = append(got, r...) }}
	fb := image.NewRGBA(image.Rect(0, 0, 300, 300))
	for i := 3; i < len(fb.Pix); i += 4 {
		fb.Pix[i] = 0xFF // the output is opaque
	}
	fb.Pix[fb.PixOffset(5, 200)] = 0xAB
	s.gfxFrame(rdpgfx.Frame{FB: fb, Dirty: []image.Rectangle{image.Rect(5, 10, 25, 290)}})
	if len(got) != 3 || got[0].Y != 10 || got[1].Y != 138 || got[2].Y != 266 || got[2].H != 24 {
		t.Fatalf("bands %+v", got)
	}
	img, err := png.Decode(bytes.NewReader(got[1].PNG))
	if err != nil {
		t.Fatal(err)
	}
	if r, _, _, _ := img.At(0, 200-138).RGBA(); r>>8 != 0xAB {
		t.Fatalf("band pixel red %x", r>>8)
	}
}

func TestGFXErrorNamesTheSwitch(t *testing.T) {
	err := gfxError(errors.Join(rdpgfx.Err))
	if !strings.Contains(err.Error(), "RXT_RDP_NO_GFX=1") {
		t.Fatal(err)
	}
	if other := errors.New("x"); gfxError(other) != other {
		t.Fatal("other errors changed")
	}
}
