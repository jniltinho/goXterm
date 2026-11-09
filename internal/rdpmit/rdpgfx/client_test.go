package rdpgfx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"math/rand"
	"os"
	"testing"
)

// raw wraps RDPGFX PDUs in an uncompressed ZGFX single segment, as a server may send them.
func raw(pdus ...[]byte) []byte {
	return append([]byte{zgfxSingle, 0x04}, bytes.Join(pdus, nil)...)
}

// pdu builds an RDPGFX PDU from little-endian fields (uint8/16/32/64 or []byte).
func pdu(cmd uint16, fields ...any) []byte {
	var b []byte
	for _, f := range fields {
		switch v := f.(type) {
		case uint8:
			b = append(b, v)
		case uint16:
			b = binary.LittleEndian.AppendUint16(b, v)
		case uint32:
			b = binary.LittleEndian.AppendUint32(b, v)
		case uint64:
			b = binary.LittleEndian.AppendUint64(b, v)
		case []byte:
			b = append(b, v...)
		default:
			panic("field type")
		}
	}
	return header(cmd, b)
}

func confirm(version, flags uint32) []byte {
	return pdu(cmdCapsConfirm, version, uint32(4), flags)
}

func reset(w, h uint32) []byte {
	return pdu(cmdResetGraphics, w, h, uint32(1), uint32(0), uint32(0), uint32(w-1), uint32(h-1), uint32(1),
		make([]byte, 340-headerSize-12-20))
}

type harness struct {
	c      *Client
	sent   [][]byte
	frames []Frame
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{}
	h.c = &Client{OnFrame: func(f Frame) {
		fb := *f.FB
		fb.Pix = append([]byte(nil), f.FB.Pix...)
		h.frames = append(h.frames, Frame{FB: &fb, Dirty: f.Dirty})
	}}
	if err := h.c.Open(func(b []byte) error { h.sent = append(h.sent, b); return nil }); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) send(t *testing.T, pdus ...[]byte) {
	t.Helper()
	if err := h.c.Data(raw(pdus...)); err != nil {
		t.Fatal(err)
	}
}

func TestCapsAdvertiseIs8And81ThinClient(t *testing.T) {
	h := newHarness(t)
	want := pdu(cmdCapsAdvertise, uint16(2), uint32(CapVersion81), uint32(4), uint32(capsThin),
		uint32(CapVersion8), uint32(4), uint32(capsThin))
	if len(h.sent) != 1 || !bytes.Equal(h.sent[0], want) {
		t.Fatalf("caps advertise % x", h.sent)
	}
}

func TestCapsConfirmChecked(t *testing.T) {
	for _, tc := range []struct {
		version, flags uint32
		ok             bool
	}{
		{CapVersion8, capsThin, true},
		{CapVersion81, capsThin, true},
		{0x000A0002, capsThin, false}, // 10.0 was never advertised
		{CapVersion81, capsThin | capsAVC420, false},
	} {
		h := newHarness(t)
		err := h.c.Data(raw(confirm(tc.version, tc.flags)))
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, Err)) {
			t.Errorf("confirm 0x%08X/0x%X: %v", tc.version, tc.flags, err)
		}
	}
	h := newHarness(t)
	if err := h.c.Data(raw(reset(64, 64))); err == nil {
		t.Error("PDU before the capabilities confirm accepted")
	}
}

// pixelAt returns the RGBA output pixel at x, y.
func pixelAt(f Frame, x, y int) [4]byte {
	o := f.FB.PixOffset(x, y)
	return [4]byte(f.FB.Pix[o : o+4])
}

func TestFakeServerSequence(t *testing.T) {
	h := newHarness(t)
	// 2×2 uncompressed BGRA at (1,1): red, green / blue, white
	bits := []byte{0, 0, 255, 0, 0, 255, 0, 0, 255, 0, 0, 0, 255, 255, 255, 0}
	h.send(t,
		confirm(CapVersion81, capsThin),
		reset(64, 32),
		pdu(cmdCreateSurface, uint16(1), uint16(40), uint16(20), uint8(pixelXRGB)),
		pdu(cmdMapSurfaceToOutput, uint16(1), uint16(0), uint32(10), uint32(5)),
	)
	h.frames = nil
	h.send(t,
		pdu(cmdStartFrame, uint32(0), uint32(7)),
		pdu(cmdSolidFill, uint16(1), []byte{0x10, 0x20, 0x30, 0}, uint16(1), uint16(0), uint16(0), uint16(40), uint16(20)),
		pdu(cmdWireToSurface1, uint16(1), uint16(codecUncompressed), uint8(pixelXRGB),
			uint16(1), uint16(1), uint16(3), uint16(3), uint32(len(bits)), bits),
	)
	if len(h.frames) != 0 {
		t.Fatal("output before the end of the frame")
	}
	h.send(t, pdu(cmdEndFrame, uint32(7)))
	if len(h.frames) != 1 {
		t.Fatalf("%d frames", len(h.frames))
	}
	f := h.frames[0]
	for _, c := range []struct {
		x, y int
		want [4]byte
	}{
		{10, 5, [4]byte{0x30, 0x20, 0x10, 0xFF}},
		{11, 6, [4]byte{255, 0, 0, 0xFF}},
		{12, 6, [4]byte{0, 255, 0, 0xFF}},
		{11, 7, [4]byte{0, 0, 255, 0xFF}},
		{12, 7, [4]byte{255, 255, 255, 0xFF}},
		{49, 24, [4]byte{0x30, 0x20, 0x10, 0xFF}},
		{50, 24, [4]byte{0, 0, 0, 0xFF}}, // outside the surface: still the reset output
	} {
		if got := pixelAt(f, c.x, c.y); got != c.want {
			t.Errorf("pixel %d,%d = %v, want %v", c.x, c.y, got, c.want)
		}
	}
	if len(f.Dirty) != 1 || f.Dirty[0] != image.Rect(10, 5, 50, 25) {
		t.Errorf("dirty %v", f.Dirty)
	}
	ack := h.sent[len(h.sent)-1]
	if !bytes.Equal(ack, frameAck(7, 1)) {
		t.Fatalf("frame ack % x", ack)
	}
	if q := binary.LittleEndian.Uint32(ack[8:]); q == 0xFFFFFFFF {
		t.Fatal("ack suspends acknowledgements")
	}
	// second frame: cache round trip and surface-to-surface, totalFramesDecoded goes on
	h.send(t,
		pdu(cmdStartFrame, uint32(0), uint32(8)),
		pdu(cmdSurfaceToCache, uint16(1), uint64(0), uint16(1), uint16(1), uint16(1), uint16(3), uint16(3)),
		pdu(cmdCacheToSurface, uint16(1), uint16(1), uint16(1), uint16(20), uint16(10)),
		pdu(cmdSurfaceToSurface, uint16(1), uint16(1), uint16(1), uint16(1), uint16(3), uint16(3), uint16(1), uint16(30), uint16(0)),
		pdu(cmdEvictCacheEntry, uint16(1)),
		pdu(cmdEndFrame, uint32(8)),
	)
	f = h.frames[1]
	if got := pixelAt(f, 30, 15); got != [4]byte{255, 0, 0, 0xFF} {
		t.Errorf("cache-to-surface pixel %v", got)
	}
	if got := pixelAt(f, 40, 6); got != [4]byte{0, 0, 255, 0xFF} {
		t.Errorf("surface-to-surface pixel %v", got)
	}
	if !bytes.Equal(h.sent[len(h.sent)-1], frameAck(8, 2)) {
		t.Fatalf("second ack % x", h.sent[len(h.sent)-1])
	}
	if err := h.c.Data(raw(pdu(cmdCacheToSurface, uint16(1), uint16(1), uint16(1), uint16(0), uint16(0)))); err == nil {
		t.Error("evicted cache slot used")
	}
}

func TestRejectedContent(t *testing.T) {
	setup := func() *harness {
		h := newHarness(t)
		h.send(t, confirm(CapVersion8, capsThin), reset(64, 64),
			pdu(cmdCreateSurface, uint16(1), uint16(16), uint16(16), uint8(pixelXRGB)))
		return h
	}
	for name, p := range map[string][]byte{
		"wire-to-surface 2": pdu(cmdWireToSurface2, uint16(1), uint16(codecProgressive), uint32(1), uint8(pixelXRGB), uint32(0)),
		"H.264":             pdu(cmdWireToSurface1, uint16(1), uint16(codecAVC420), uint8(pixelXRGB), uint16(0), uint16(0), uint16(1), uint16(1), uint32(0)),
		"unknown codec":     pdu(cmdWireToSurface1, uint16(1), uint16(0x0042), uint8(pixelXRGB), uint16(0), uint16(0), uint16(1), uint16(1), uint32(0)),
		"outside surface":   pdu(cmdWireToSurface1, uint16(1), uint16(codecUncompressed), uint8(pixelXRGB), uint16(0), uint16(0), uint16(17), uint16(1), uint32(68), make([]byte, 68)),
		"short data":        pdu(cmdWireToSurface1, uint16(1), uint16(codecUncompressed), uint8(pixelXRGB), uint16(0), uint16(0), uint16(2), uint16(2), uint32(4), make([]byte, 4)),
		"unknown surface":   pdu(cmdSolidFill, uint16(9), uint32(0), uint16(0)),
		"huge surface":      pdu(cmdCreateSurface, uint16(2), uint16(9000), uint16(10), uint8(pixelXRGB)),
		"cache slot 0":      pdu(cmdSurfaceToCache, uint16(1), uint64(0), uint16(0), uint16(0), uint16(0), uint16(1), uint16(1)),
		"cache slot 4097":   pdu(cmdSurfaceToCache, uint16(1), uint64(0), uint16(4097), uint16(0), uint16(0), uint16(1), uint16(1)),
		"truncated":         header(cmdSolidFill, []byte{1}),
		"unknown PDU":       pdu(0x0014),
	} {
		h := setup()
		if err := h.c.Data(raw(p)); err == nil || !errors.Is(err, Err) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestMalformedPDUsNeverPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	cmds := []uint16{cmdWireToSurface1, cmdSolidFill, cmdSurfaceToSurface, cmdSurfaceToCache, cmdCacheToSurface,
		cmdEvictCacheEntry, cmdCreateSurface, cmdDeleteSurface, cmdStartFrame, cmdEndFrame, cmdResetGraphics,
		cmdMapSurfaceToOutput, cmdDeleteEncodingCtx}
	for i := 0; i < 30000; i++ {
		h := newHarness(t)
		h.send(t, confirm(CapVersion81, capsThin), reset(64, 64),
			pdu(cmdCreateSurface, uint16(1), uint16(32), uint16(32), uint8(pixelARGB)),
			pdu(cmdMapSurfaceToOutput, uint16(1), uint16(0), uint32(0), uint32(0)))
		body := make([]byte, rng.Intn(40))
		rng.Read(body)
		for j := 0; j+1 < len(body) && j < 6; j += 2 { // small ids and coordinates hit real paths
			body[j+1] = 0
		}
		_ = h.c.Data(raw(header(cmds[rng.Intn(len(cmds))], body)))
		p := make([]byte, rng.Intn(20))
		rng.Read(p)
		_ = h.c.Data(p)
	}
}

func TestResetGraphicsAndClose(t *testing.T) {
	h := newHarness(t)
	h.send(t, confirm(CapVersion81, capsThin), reset(32, 32),
		pdu(cmdCreateSurface, uint16(1), uint16(16), uint16(16), uint8(pixelXRGB)),
		pdu(cmdMapSurfaceToOutput, uint16(1), uint16(0), uint32(0), uint32(0)),
		pdu(cmdSolidFill, uint16(1), []byte{0x10, 0x20, 0x30, 0}, uint16(1), uint16(0), uint16(0), uint16(16), uint16(16)),
		pdu(cmdSurfaceToCache, uint16(1), uint64(0), uint16(4096), uint16(0), uint16(0), uint16(4), uint16(4)))
	h.frames = nil
	// a new desktop size: the output follows it, surfaces and cache survive, surfaces are cleared
	h.send(t, reset(48, 40))
	if len(h.frames) != 1 || h.frames[0].FB.Bounds() != image.Rect(0, 0, 48, 40) || h.frames[0].Dirty[0] != image.Rect(0, 0, 48, 40) {
		t.Fatalf("frames after reset: %+v", h.frames)
	}
	h.send(t, pdu(cmdCacheToSurface, uint16(4096), uint16(1), uint16(1), uint16(8), uint16(8)),
		pdu(cmdSolidFill, uint16(1), []byte{0, 0, 0, 0}, uint16(1), uint16(0), uint16(0), uint16(1), uint16(1)))
	f := h.frames[len(h.frames)-1]
	// the cleared surface is not redrawn until the server updates it (as FreeRDP)
	if pixelAt(f, 8, 8) != [4]byte{0x30, 0x20, 0x10, 0xFF} || pixelAt(f, 4, 4) != [4]byte{0, 0, 0, 0xFF} {
		t.Fatalf("after reset: %v %v", pixelAt(f, 8, 8), pixelAt(f, 4, 4))
	}
	// closing the channel (reactivation) drops everything: a new channel starts with caps again
	h.c.Close()
	if err := h.c.Data(raw(pdu(cmdDeleteSurface, uint16(1)))); err == nil {
		t.Fatal("PDU accepted on a closed channel before caps")
	}
	h.send(t, confirm(CapVersion8, capsThin))
	if err := h.c.Data(raw(pdu(cmdEvictCacheEntry, uint16(4096)))); err != nil {
		t.Fatal(err)
	}
	if err := h.c.Data(raw(pdu(cmdCacheToSurface, uint16(4096), uint16(1), uint16(0)))); err == nil {
		t.Fatal("state survived the channel close")
	}
}

func TestClearCodecOnSurface(t *testing.T) {
	h := newHarness(t)
	// 2×1 ClearCodec bitmap: residual of two red pixels
	cc := []byte{0, 1, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 255, 2}
	h.send(t, confirm(CapVersion81, capsThin), reset(16, 16),
		pdu(cmdCreateSurface, uint16(1), uint16(16), uint16(16), uint8(pixelXRGB)),
		pdu(cmdMapSurfaceToOutput, uint16(1), uint16(0), uint32(0), uint32(0)),
		pdu(cmdStartFrame, uint32(0), uint32(1)),
		pdu(cmdWireToSurface1, uint16(1), uint16(codecClear), uint8(pixelXRGB),
			uint16(3), uint16(4), uint16(5), uint16(5), uint32(len(cc)), cc),
		pdu(cmdEndFrame, uint32(1)))
	f := h.frames[len(h.frames)-1]
	if pixelAt(f, 3, 4) != [4]byte{255, 0, 0, 255} || pixelAt(f, 4, 4) != [4]byte{255, 0, 0, 255} {
		t.Fatalf("pixels %v %v", pixelAt(f, 3, 4), pixelAt(f, 4, 4))
	}
	// a broken ClearCodec stream ends the session
	if err := h.c.Data(raw(pdu(cmdWireToSurface1, uint16(1), uint16(codecClear), uint8(pixelXRGB),
		uint16(0), uint16(0), uint16(1), uint16(1), uint32(2), []byte{0, 7}))); err == nil || !errors.Is(err, Err) {
		t.Fatalf("bad stream: %v", err)
	}
}

func TestAlphaCodec(t *testing.T) {
	h := newHarness(t)
	h.send(t, confirm(CapVersion81, capsThin), reset(16, 16),
		pdu(cmdCreateSurface, uint16(1), uint16(16), uint16(16), uint8(pixelARGB)))
	s := h.c.surfaces[1]
	alphaAt := func(x, y int) byte { return s.pix[(y*s.pw+x)*4+3] }
	// raw 2×2 at (1,1), then RLE 3×1 at (0,5): 0x10 ×2, 0x20 ×1 (with the 0xFF long-count form)
	h.send(t, pdu(cmdWireToSurface1, uint16(1), uint16(codecAlpha), uint8(pixelARGB),
		uint16(1), uint16(1), uint16(3), uint16(3), uint32(8), []byte{0x4C, 0x41, 0, 0, 1, 2, 3, 4}))
	h.send(t, pdu(cmdWireToSurface1, uint16(1), uint16(codecAlpha), uint8(pixelARGB),
		uint16(0), uint16(5), uint16(3), uint16(6), uint32(10), []byte{0x4C, 0x41, 1, 0, 0x10, 0xFF, 2, 0, 0x20, 1}))
	if alphaAt(1, 1) != 1 || alphaAt(2, 2) != 4 || alphaAt(0, 5) != 0x10 || alphaAt(1, 5) != 0x10 || alphaAt(2, 5) != 0x20 || alphaAt(3, 5) != 0xFF {
		t.Fatalf("alpha %d %d %d %d %d %d", alphaAt(1, 1), alphaAt(2, 2), alphaAt(0, 5), alphaAt(1, 5), alphaAt(2, 5), alphaAt(3, 5))
	}
	if err := h.c.Data(raw(pdu(cmdWireToSurface1, uint16(1), uint16(codecAlpha), uint8(pixelARGB),
		uint16(0), uint16(0), uint16(2), uint16(1), uint32(6), []byte{0x4C, 0x41, 1, 0, 9, 3}))); err == nil {
		t.Fatal("alpha run past the destination accepted")
	}
}

func TestRemoteFXOnSurface(t *testing.T) {
	h := newHarness(t)
	hdr, _ := os.ReadFile("../codec/rfx/testdata/msrdprfx-header.bin")
	data, _ := os.ReadFile("../codec/rfx/testdata/msrdprfx-data.bin")
	msg := append(hdr, data...)
	h.send(t, confirm(CapVersion81, capsThin), reset(128, 128),
		pdu(cmdCreateSurface, uint16(1), uint16(128), uint16(128), uint8(pixelXRGB)),
		pdu(cmdMapSurfaceToOutput, uint16(1), uint16(0), uint32(0), uint32(0)))
	h.send(t, pdu(cmdStartFrame, uint32(0), uint32(3)),
		pdu(cmdWireToSurface1, uint16(1), uint16(codecCAVideo), uint8(pixelXRGB),
			uint16(32), uint16(16), uint16(96), uint16(80), uint32(len(msg)), msg),
		pdu(cmdEndFrame, uint32(3)))
	f := h.frames[len(h.frames)-1]
	if len(f.Dirty) != 1 || f.Dirty[0] != image.Rect(32, 16, 96, 80) {
		t.Fatalf("dirty %v", f.Dirty)
	}
	// reference pixel 0 of MS-RDPRFX 4.2.4.4 is 0x229CDF, drawn at the destination origin
	if got := pixelAt(f, 32, 16); got[0] < 0x21 || got[0] > 0x23 || got[2] < 0xDE || got[2] > 0xE0 {
		t.Fatalf("pixel %v", got)
	}
}
