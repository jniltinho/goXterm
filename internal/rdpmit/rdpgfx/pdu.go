// rustxterm addition (not in upstream go-rdp): RDP Graphics Pipeline (MS-RDPEGFX) client, version
// 8 / 8.1 without AVC, written from the specification.

// Package rdpgfx is the client side of the RDP Graphics Pipeline dynamic channel: ZGFX
// decompression, capabilities, surfaces, the surface cache, codecs and frame acknowledgements.
package rdpgfx

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ChannelName is the dynamic virtual channel of the graphics pipeline.
const ChannelName = "Microsoft::Windows::RDS::Graphics"

// RDPGFX_CMDID_* (MS-RDPEGFX 2.2.1.5).
const (
	cmdWireToSurface1      = 0x0001
	cmdWireToSurface2      = 0x0002
	cmdDeleteEncodingCtx   = 0x0003
	cmdSolidFill           = 0x0004
	cmdSurfaceToSurface    = 0x0005
	cmdSurfaceToCache      = 0x0006
	cmdCacheToSurface      = 0x0007
	cmdEvictCacheEntry     = 0x0008
	cmdCreateSurface       = 0x0009
	cmdDeleteSurface       = 0x000A
	cmdStartFrame          = 0x000B
	cmdEndFrame            = 0x000C
	cmdFrameAcknowledge    = 0x000D
	cmdResetGraphics       = 0x000E
	cmdMapSurfaceToOutput  = 0x000F
	cmdCacheImportReply    = 0x0011
	cmdCapsAdvertise       = 0x0012
	cmdCapsConfirm         = 0x0013
	cmdMapSurfaceToWindow  = 0x0015
	cmdMapSurfaceToScaled  = 0x0017
	cmdMapSurfaceToScaledW = 0x0018

	headerSize = 8

	// CapVersion8 and CapVersion81 are the only versions advertised (RDPGFX_CAPVERSION_8 / _81).
	CapVersion8  = 0x00080004
	CapVersion81 = 0x00080105
	capsThin     = 0x00000001 // RDPGFX_CAPS_FLAG_THINCLIENT: no progressive codec, 16 MiB cache
	capsAVC420   = 0x00000010 // never set: no H.264

	queueDepthUnavailable = 0
)

// Err is wrapped by every graphics pipeline error, so the session can name the troubleshooting
// switch.
var Err = errors.New("RDP graphics pipeline")

func fail(format string, a ...any) error {
	return fmt.Errorf("%w: %s", Err, fmt.Sprintf(format, a...))
}

// header builds an RDPGFX_HEADER followed by body.
func header(cmd uint16, body []byte) []byte {
	b := make([]byte, headerSize, headerSize+len(body))
	binary.LittleEndian.PutUint16(b, cmd)
	binary.LittleEndian.PutUint32(b[4:], uint32(headerSize+len(body)))
	return append(b, body...)
}

// capsAdvertise is the client's RDPGFX_CAPS_ADVERTISE_PDU: 8.1 and 8, both THINCLIENT, no AVC.
func capsAdvertise() []byte {
	b := binary.LittleEndian.AppendUint16(nil, 2)
	for _, v := range []uint32{CapVersion81, CapVersion8} {
		b = binary.LittleEndian.AppendUint32(b, v)
		b = binary.LittleEndian.AppendUint32(b, 4)
		b = binary.LittleEndian.AppendUint32(b, capsThin)
	}
	return header(cmdCapsAdvertise, b)
}

// frameAck is an RDPGFX_FRAME_ACKNOWLEDGE_PDU.
func frameAck(frameID, totalDecoded uint32) []byte {
	b := binary.LittleEndian.AppendUint32(nil, queueDepthUnavailable)
	b = binary.LittleEndian.AppendUint32(b, frameID)
	b = binary.LittleEndian.AppendUint32(b, totalDecoded)
	return header(cmdFrameAcknowledge, b)
}

// reader is a bounds-checked little-endian reader over one PDU body; the first short read sets
// bad and every later read returns zero.
type reader struct {
	p   []byte
	bad bool
}

func (r *reader) take(n int) []byte {
	if r.bad || n < 0 || n > len(r.p) {
		r.bad = true
		return nil
	}
	b := r.p[:n]
	r.p = r.p[n:]
	return b
}

func (r *reader) u8() uint8 {
	if b := r.take(1); b != nil {
		return b[0]
	}
	return 0
}

func (r *reader) u16() uint16 {
	if b := r.take(2); b != nil {
		return binary.LittleEndian.Uint16(b)
	}
	return 0
}

func (r *reader) u32() uint32 {
	if b := r.take(4); b != nil {
		return binary.LittleEndian.Uint32(b)
	}
	return 0
}

// rect16 reads an RDPGFX_RECT16 (left, top, right, bottom; right/bottom exclusive).
func (r *reader) rect16() rect {
	return rect{int(r.u16()), int(r.u16()), int(r.u16()), int(r.u16())}
}

// rect is a half-open rectangle [x0,x1) × [y0,y1).
type rect struct{ x0, y0, x1, y1 int }

func (a rect) empty() bool { return a.x0 >= a.x1 || a.y0 >= a.y1 }

func (a rect) w() int { return a.x1 - a.x0 }

func (a rect) h() int { return a.y1 - a.y0 }

func (a rect) intersect(b rect) rect {
	r := rect{max(a.x0, b.x0), max(a.y0, b.y0), min(a.x1, b.x1), min(a.y1, b.y1)}
	if r.empty() {
		return rect{}
	}
	return r
}

func (a rect) union(b rect) rect {
	if a.empty() {
		return b
	}
	if b.empty() {
		return a
	}
	return rect{min(a.x0, b.x0), min(a.y0, b.y0), max(a.x1, b.x1), max(a.y1, b.y1)}
}

// within reports whether a lies inside a w×h area.
func (a rect) within(w, h int) bool {
	return a.x0 >= 0 && a.y0 >= 0 && a.x0 <= a.x1 && a.y0 <= a.y1 && a.x1 <= w && a.y1 <= h
}
