// rustxterm addition (replaces upstream's message.go, whose block offsets left out the codecId and
// channelId of codec channel blocks and treated the tileset subtype as a block type, so no message
// from a real server decoded): RemoteFX messages (MS-RDPRFX 2.2.2) as the graphics pipeline sends
// them (RDPGFX_CODECID_CAVIDEO), written from the specification and checked against FreeRDP's
// rfx_process_message.

package rfx

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"runtime"
	"sync"
)

const (
	cbtRegion  = 0xCAC1
	cbtTileset = 0xCAC2
	wfMagic    = 0xCACCACCA
	wfVersion  = 0x0100

	seenSync     = 1
	seenContext  = 2
	seenVersions = 4
	seenChannels = 8
	seenHeaders  = seenSync | seenContext | seenVersions | seenChannels
)

var errMessage = errors.New("rfx: malformed message")

// Decoder keeps the RemoteFX header state (entropy mode, channel size) across messages of one
// graphics channel.
type Decoder struct {
	seen          int
	mode          int
	width, height int
	expect        uint16 // next data block: WBT_FRAME_BEGIN, then REGION, EXTENSION, FRAME_END
}

// Decode draws the tiles of one RemoteFX message into dst (RGBA rows of stride bytes, dw×dh pixels)
// with the message origin at left, top, clipped by the message region and by dst. It returns the
// rectangles it changed.
func (d *Decoder) Decode(data, dst []byte, stride, dw, dh, left, top int) ([]image.Rectangle, error) {
	if d.expect == 0 {
		d.expect = WBT_FRAME_BEGIN
	}
	var (
		rects []image.Rectangle // region, absolute and clipped to dst
		tiles []tileRef
		quant []*SubbandQuant
	)
	bounds := image.Rect(0, 0, dw, dh)
	for len(data) > 6 {
		typ := binary.LittleEndian.Uint16(data)
		n := binary.LittleEndian.Uint32(data[2:])
		if n < 6 || uint64(n) > uint64(len(data)) {
			return nil, errMessage
		}
		block := data[6:n]
		data = data[n:]
		if typ > WBT_CONTEXT && d.seen != seenHeaders {
			return nil, errors.New("rfx: data before the header blocks")
		}
		if typ >= WBT_CONTEXT && typ <= WBT_EXTENSION { // codec channel blocks: codecId, channelId
			wantChannel := byte(0)
			if typ == WBT_CONTEXT {
				wantChannel = 0xFF
			}
			if len(block) < 2 || block[0] != 1 || block[1] != wantChannel {
				return nil, errMessage
			}
			block = block[2:]
		}
		var err error
		switch typ {
		case WBT_SYNC:
			if len(block) < 6 || binary.LittleEndian.Uint32(block) != wfMagic || binary.LittleEndian.Uint16(block[4:]) != wfVersion {
				return nil, errMessage
			}
			d.seen |= seenSync
		case WBT_CODEC_VERSIONS:
			if len(block) < 4 || block[0] != 1 || block[1] != 1 || binary.LittleEndian.Uint16(block[2:]) != wfVersion {
				return nil, errMessage
			}
			d.seen |= seenVersions
		case WBT_CHANNELS:
			if len(block) < 1 || block[0] < 1 || len(block) < 1+5*int(block[0]) {
				return nil, errMessage
			}
			d.width, d.height = int(binary.LittleEndian.Uint16(block[2:])), int(binary.LittleEndian.Uint16(block[4:]))
			d.seen |= seenChannels
		case WBT_CONTEXT:
			if len(block) < 5 {
				return nil, errMessage
			}
			switch binary.LittleEndian.Uint16(block[3:]) >> 9 & 0xF {
			case uint16(CLW_ENTROPY_RLGR1):
				d.mode = RLGR1
			case uint16(CLW_ENTROPY_RLGR3):
				d.mode = RLGR3
			default:
				return nil, errors.New("rfx: unknown entropy algorithm")
			}
			d.seen |= seenContext
		case WBT_FRAME_BEGIN:
			err = d.next(WBT_FRAME_BEGIN, WBT_REGION)
		case WBT_REGION:
			if err = d.next(WBT_REGION, WBT_EXTENSION); err == nil {
				rects, err = d.region(block, left, top, bounds)
			}
		case WBT_EXTENSION:
			if err = d.next(WBT_EXTENSION, WBT_FRAME_END); err == nil {
				tiles, quant, err = tileset(block)
			}
		case WBT_FRAME_END:
			err = d.next(WBT_FRAME_END, WBT_FRAME_BEGIN)
		default:
			err = fmt.Errorf("rfx: block type 0x%04X", typ)
		}
		if err != nil {
			return nil, err
		}
	}
	return d.draw(tiles, quant, rects, dst, stride, left, top)
}

func (d *Decoder) next(got, then uint16) error {
	if d.expect != got {
		d.expect = WBT_FRAME_BEGIN
		return fmt.Errorf("rfx: block 0x%04X out of order", got)
	}
	d.expect = then
	return nil
}

// region reads TS_RFX_REGION: rectangles relative to the message origin (none means the whole
// channel).
func (d *Decoder) region(b []byte, left, top int, bounds image.Rectangle) ([]image.Rectangle, error) {
	if len(b) < 3 {
		return nil, errMessage
	}
	n := int(binary.LittleEndian.Uint16(b[1:]))
	b = b[3:]
	if n == 0 {
		return []image.Rectangle{image.Rect(left, top, left+d.width, top+d.height).Intersect(bounds)}, nil
	}
	if len(b) < 8*n+4 {
		return nil, errMessage
	}
	out := make([]image.Rectangle, 0, n)
	for i := range n {
		x, y := int(binary.LittleEndian.Uint16(b[8*i:])), int(binary.LittleEndian.Uint16(b[8*i+2:]))
		w, h := int(binary.LittleEndian.Uint16(b[8*i+4:])), int(binary.LittleEndian.Uint16(b[8*i+6:]))
		if r := image.Rect(left+x, top+y, left+x+w, top+y+h).Intersect(bounds); !r.Empty() {
			out = append(out, r)
		}
	}
	b = b[8*n:]
	if binary.LittleEndian.Uint16(b) == cbtRegion && binary.LittleEndian.Uint16(b[2:]) != 1 {
		return nil, errors.New("rfx: region with more than one tileset")
	}
	return out, nil
}

// tileRef is one CBT_TILE block, still encoded.
type tileRef struct {
	data       []byte
	x, y       int // pixel offset from the message origin
	qy, qb, qr int
}

// tileset reads TS_RFX_TILESET: quantization tables and tiles (decoded later).
func tileset(b []byte) ([]tileRef, []*SubbandQuant, error) {
	if len(b) < 14 || binary.LittleEndian.Uint16(b) != cbtTileset {
		return nil, nil, errMessage
	}
	numQuant := int(b[6])
	numTiles := int(binary.LittleEndian.Uint16(b[8:]))
	size := binary.LittleEndian.Uint32(b[10:])
	b = b[14:]
	if numQuant < 1 || len(b) < 5*numQuant {
		return nil, nil, errMessage
	}
	if numTiles == 0 { // Windows Server 2012 sends empty tilesets
		return nil, nil, nil
	}
	quant := make([]*SubbandQuant, numQuant)
	for i := range quant {
		quant[i], _ = ParseQuantValues(b[5*i:])
	}
	b = b[5*numQuant:]
	if uint64(size) > uint64(len(b)) {
		return nil, nil, errMessage
	}
	tiles := make([]tileRef, 0, min(numTiles, len(b)/19))
	for range numTiles {
		if len(b) < 19 || binary.LittleEndian.Uint16(b) != CBT_TILE {
			return nil, nil, errMessage
		}
		n := binary.LittleEndian.Uint32(b[2:])
		if n < 19 || uint64(n) > uint64(len(b)) {
			return nil, nil, errMessage
		}
		t := tileRef{data: b[:n], qy: int(b[6]), qb: int(b[7]), qr: int(b[8]),
			x: 64 * int(binary.LittleEndian.Uint16(b[9:])), y: 64 * int(binary.LittleEndian.Uint16(b[11:]))}
		if t.qy >= numQuant || t.qb >= numQuant || t.qr >= numQuant {
			return nil, nil, errors.New("rfx: tile quantization index out of range")
		}
		tiles = append(tiles, t)
		b = b[n:]
	}
	return tiles, quant, nil
}

// draw decodes the tiles that meet the region, in parallel, and copies their visible parts.
func (d *Decoder) draw(tiles []tileRef, quant []*SubbandQuant, rects []image.Rectangle, dst []byte, stride, left, top int) ([]image.Rectangle, error) {
	var (
		mu      sync.Mutex
		changed []image.Rectangle
		first   error
		wg      sync.WaitGroup
	)
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for _, t := range tiles {
		area := image.Rect(left+t.x, top+t.y, left+t.x+TileSize, top+t.y+TileSize)
		var parts []image.Rectangle
		for _, r := range rects {
			if p := r.Intersect(area); !p.Empty() {
				parts = append(parts, p)
			}
		}
		if len(parts) == 0 {
			continue // not visible: not decoded
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(t tileRef, area image.Rectangle, parts []image.Rectangle) {
			defer func() { <-sem; wg.Done() }()
			tile, err := DecodeTile(t.data, quant[t.qy], quant[t.qb], quant[t.qr], d.mode)
			if err != nil {
				mu.Lock()
				first = cmpErr(first, err)
				mu.Unlock()
				return
			}
			for _, p := range parts { // tiles never overlap, so the writes do not either
				for y := p.Min.Y; y < p.Max.Y; y++ {
					src := ((y-area.Min.Y)*TileSize + p.Min.X - area.Min.X) * 4
					copy(dst[y*stride+p.Min.X*4:y*stride+p.Max.X*4], tile.RGBA[src:])
				}
			}
			mu.Lock()
			changed = append(changed, parts...)
			mu.Unlock()
		}(t, area, parts)
	}
	wg.Wait()
	if first != nil {
		return nil, first
	}
	return changed, nil
}

func cmpErr(old, err error) error {
	if old != nil {
		return old
	}
	return err
}
