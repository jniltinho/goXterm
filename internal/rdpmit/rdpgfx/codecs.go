package rdpgfx

import (
	"errors"

	"rustxterm-go/internal/rdpmit/codec"
	"rustxterm-go/internal/rdpmit/codec/clear"
)

var errShortData = errors.New("bitmap data too short")

// codecState is the codec context kept for the whole channel (ClearCodec caches, RemoteFX
// quantization and context).
type codecState struct {
	clear *clear.Decoder
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

func (c *Client) remoteFX(s *surface, dst rect, data []byte) ([]rect, error) {
	return nil, errors.New("RemoteFX is not implemented yet")
}

func alphaCodec(s *surface, dst rect, data []byte) error {
	return errors.New("the alpha codec is not implemented yet")
}
