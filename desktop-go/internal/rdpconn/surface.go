package rdpconn

import (
	"encoding/binary"

	"rustxterm-go/internal/rdpmit/codec"
	"rustxterm-go/internal/rdpmit/protocol/pdu"
)

// Surface commands (MS-RDPBCGR 2.2.9.2): RDP 8+ servers send the desktop as NSCodec surface bits
// when the client offers the codec (rdp.Client.SetEnableRFX).
const (
	fpUpdateSurfCmds   = 0x4    // FASTPATH_UPDATETYPE_SURFCMDS
	cmdSurfaceBits     = 0x0001 // CMDTYPE_SET_SURFACE_BITS
	cmdFrameMarker     = 0x0004 // CMDTYPE_FRAME_MARKER
	cmdStreamSurfBits  = 0x0006 // CMDTYPE_STREAM_SURFACE_BITS
	frameEnd           = 0x0001 // SURFACECMD_FRAMEACTION_END
	exHeaderPresent    = 0x01   // EX_COMPRESSED_BITMAP_HEADER_PRESENT
	exHeaderSize       = 24     // TS_COMPRESSED_BITMAP_HEADER_EX
	surfaceBitsHdrSize = 20     // destLeft..bitmapDataLength after cmdType
)

// tileJob is a decoded top-down RGBA bitmap to put on screen at (x, y).
type tileJob struct {
	rgba []byte
	w, h int
	x, y int
}

// surfaceCommands decodes one TS_FP_SURFCMDS body into screen jobs and returns the ids of frames
// that ended (each must be acknowledged).
func surfaceCommands(p []byte) (jobs []tileJob, ended []uint32) {
	for len(p) >= 2 {
		cmd := binary.LittleEndian.Uint16(p)
		p = p[2:]
		switch cmd {
		case cmdSurfaceBits, cmdStreamSurfBits:
			if len(p) < surfaceBitsHdrSize {
				return
			}
			left, top := int(binary.LittleEndian.Uint16(p)), int(binary.LittleEndian.Uint16(p[2:]))
			flags, codecID := p[9], p[11]
			w, h := int(binary.LittleEndian.Uint16(p[12:])), int(binary.LittleEndian.Uint16(p[14:]))
			n := int(binary.LittleEndian.Uint32(p[16:]))
			p = p[surfaceBitsHdrSize:]
			if n < 0 || n > len(p) {
				return
			}
			data := p[:n]
			p = p[n:]
			if flags&exHeaderPresent != 0 {
				if len(data) < exHeaderSize {
					continue
				}
				data = data[exHeaderSize:]
			}
			if codecID != pdu.NSCodecID || w <= 0 || h <= 0 {
				continue
			}
			if rgba := codec.DecodeNSCodecToRGBA(data, w, h); rgba != nil {
				jobs = append(jobs, tileJob{rgba: rgba, w: w, h: h, x: left, y: top})
			}
		case cmdFrameMarker:
			if len(p) < 6 {
				return
			}
			if binary.LittleEndian.Uint16(p) == frameEnd {
				ended = append(ended, binary.LittleEndian.Uint32(p[2:]))
			}
			p = p[6:]
		default: // unknown command: its length is unknown, so the rest cannot be parsed
			return
		}
	}
	return
}
