package rdpconn

import (
	"encoding/binary"
	"testing"
)

// surfaceBits builds a CMDTYPE_SET_SURFACE_BITS with an optional extended header.
func surfaceBits(left, top, w, h, bpp int, codecID byte, exHdr bool, data []byte) []byte {
	b := binary.LittleEndian.AppendUint16(nil, cmdSurfaceBits)
	for _, v := range []int{left, top, left + w, top + h} {
		b = binary.LittleEndian.AppendUint16(b, uint16(v))
	}
	flags := byte(0)
	if exHdr {
		flags = exHeaderPresent
	}
	b = append(b, byte(bpp), flags, 0, codecID)
	b = binary.LittleEndian.AppendUint16(b, uint16(w))
	b = binary.LittleEndian.AppendUint16(b, uint16(h))
	b = binary.LittleEndian.AppendUint32(b, uint32(len(data))) // excludes the extended header
	if exHdr {
		b = append(b, make([]byte, exHeaderSize)...)
	}
	return append(b, data...)
}

func frameMarker(action uint16, id uint32) []byte {
	b := binary.LittleEndian.AppendUint16(nil, cmdFrameMarker)
	b = binary.LittleEndian.AppendUint16(b, action)
	return binary.LittleEndian.AppendUint32(b, id)
}

// TestSurfaceCommands: an uncompressed 1×2 BGRA surface with the extended header (not counted in
// bitmapDataLength) is decoded bottom-up, and the frame marker after it is still parsed.
func TestSurfaceCommands(t *testing.T) {
	px := []byte{1, 2, 3, 0, 10, 20, 30, 0} // BGRA rows, bottom row first
	p := append(surfaceBits(5, 6, 1, 2, 32, codecNone, true, px), frameMarker(frameEnd, 42)...)
	jobs, ended := surfaceCommands(p, &pixelBudget{rect: 1 << 20, left: 1 << 20})
	if len(jobs) != 1 || len(ended) != 1 || ended[0] != 42 {
		t.Fatalf("jobs %d ended %v", len(jobs), ended)
	}
	j := jobs[0]
	if j.x != 5 || j.y != 6 || j.w != 1 || j.h != 2 {
		t.Fatalf("job at %d,%d %dx%d", j.x, j.y, j.w, j.h)
	}
	if got := j.rgba; got[0] != 30 || got[1] != 20 || got[2] != 10 || got[4] != 3 || got[7] != 255 {
		t.Fatalf("rgba %v: want the top row (30,20,10) first", got)
	}
}

// TestSurfaceBudget: a 65535×65535 surface (a hostile server) is skipped before any allocation,
// and the frame marker after it still counts.
func TestSurfaceBudget(t *testing.T) {
	p := append(surfaceBits(0, 0, 65535, 65535, 32, codecNone, false, nil), frameMarker(frameEnd, 7)...)
	jobs, ended := surfaceCommands(p, &pixelBudget{rect: 1 << 20, left: 1 << 20})
	if len(jobs) != 0 || len(ended) != 1 {
		t.Fatalf("jobs %d ended %v", len(jobs), ended)
	}
}
