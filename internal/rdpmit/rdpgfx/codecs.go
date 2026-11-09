package rdpgfx

import (
	"encoding/binary"
	"errors"

	"rustxterm-go/internal/rdpmit/codec"
	"rustxterm-go/internal/rdpmit/codec/clear"
	"rustxterm-go/internal/rdpmit/codec/rfx"
)

var errShortData = errors.New("bitmap data too short")

// codecState is the codec context kept for the whole channel (ClearCodec caches, RemoteFX
// quantization and context).
type codecState struct {
	clear *clear.Decoder
	rfx   rfx.Decoder
}

func (c *Client) planar(s *surface, dst rect, data []byte) error {
	rgba, err := codec.DecodePlanarGFX(data, dst.w(), dst.h())
	if err != nil {
		return err
	}
	s.blit(dst, rgba)
	return nil
}

func (c *Client) clear(s *surface, dst rect, data []byte) ([]rect, error) {
	if c.codecs.clear == nil {
		c.codecs.clear = &clear.Decoder{}
	}
	return nil, c.codecs.clear.Decode(data, s.pix, s.pw*4, dst.x0, dst.y0, dst.w(), dst.h())
}

// remoteFX is RDPGFX_CODECID_CAVIDEO: a RemoteFX message whose origin is the destination's top
// left; its region (not the destination rectangle) says what changes, clipped to the surface.
func (c *Client) remoteFX(s *surface, dst rect, data []byte) ([]rect, error) {
	changed, err := c.codecs.rfx.Decode(data, s.pix, s.pw*4, s.pw, s.ph, dst.x0, dst.y0)
	if err != nil {
		return nil, err
	}
	out := make([]rect, 0, len(changed)+1)
	for _, r := range changed {
		out = append(out, rect{r.Min.X, r.Min.Y, r.Max.X, r.Max.Y})
	}
	if len(out) == 0 {
		out = append(out, rect{}) // nothing visible changed: not the whole destination either
	}
	return out, nil
}

// alphaCodec is RDPGFX_CODECID_ALPHA (MS-RDPEGFX 2.2.4.3): it replaces the alpha channel of the
// destination, raw or run-length encoded.
func alphaCodec(s *surface, dst rect, data []byte) error {
	if len(data) < 4 || binary.LittleEndian.Uint16(data) != 0x414C {
		return errors.New("alpha codec: bad signature")
	}
	compressed := binary.LittleEndian.Uint16(data[2:]) != 0
	data = data[4:]
	w, total := dst.w(), dst.w()*dst.h()
	set := func(i int, a byte) { s.pix[((dst.y0+i/w)*s.pw+dst.x0+i%w)*4+3] = a }
	if !compressed {
		if len(data) < total {
			return errShortData
		}
		for i := range total {
			set(i, data[i])
		}
		return nil
	}
	for i := 0; i < total; {
		if len(data) < 2 {
			return errShortData
		}
		a, n := data[0], int(data[1])
		data = data[2:]
		if n == 0xFF {
			if len(data) < 2 {
				return errShortData
			}
			n, data = int(binary.LittleEndian.Uint16(data)), data[2:]
			if n == 0xFFFF {
				if len(data) < 4 {
					return errShortData
				}
				n, data = int(binary.LittleEndian.Uint32(data)), data[4:]
			}
		}
		if n > total-i {
			return errors.New("alpha codec: run past the destination")
		}
		for ; n > 0; n, i = n-1, i+1 {
			set(i, a)
		}
	}
	return nil
}
