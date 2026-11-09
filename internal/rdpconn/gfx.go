package rdpconn

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"

	"rustxterm-go/internal/rdpmit/protocol/drdynvc"
	"rustxterm-go/internal/rdpmit/rdp"
	"rustxterm-go/internal/rdpmit/rdpgfx"
)

// noGFXEnv disables the graphics pipeline (MS-RDPEGFX) for troubleshooting: once the server opens
// it, it sends no bitmap updates, so a pipeline failure can only be worked around by not
// advertising it.
const noGFXEnv = "RXT_RDP_NO_GFX"

func gfxEnabled() bool { return os.Getenv(noGFXEnv) != "1" }

// bandRows splits large changed areas into bands that are PNG-encoded in parallel.
const bandRows = 128

// startGFX joins drdynvc and advertises the graphics pipeline on c.
func (s *Session) startGFX(c *rdp.Client) {
	dvc := &drdynvc.Client{
		Send: func(chunk []byte) error { return c.SendStaticChannel(drdynvc.ChannelName, chunk) },
		Accept: func(name string) drdynvc.Channel {
			if name != rdpgfx.ChannelName {
				return nil
			}
			return &rdpgfx.Client{
				OnFrame: s.gfxFrame,
				OnOpen: func(version, flags uint32) {
					// once per channel, at info: go-rdp's own logging is held at error level (rdpconn.go)
					log.Printf("rdp: graphics pipeline open (version 0x%08X, flags 0x%X)", version, flags)
				},
			}
		},
	}
	c.AddStaticChannel(drdynvc.ChannelName, drdynvc.Options, dvc.Receive)
	c.SetEnableGFX(true)
}

// gfxFrame PNG-encodes the changed areas of a graphics pipeline frame and hands them over.
func (s *Session) gfxFrame(f rdpgfx.Frame) {
	var bands []image.Rectangle
	for _, r := range f.Dirty {
		for y := r.Min.Y; y < r.Max.Y; y += bandRows {
			bands = append(bands, image.Rect(r.Min.X, y, r.Max.X, min(y+bandRows, r.Max.Y)))
		}
	}
	rects := parallelRects(len(bands), func(i int) (Rect, bool) {
		b := bands[i]
		var buf bytes.Buffer
		if (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, f.FB.SubImage(b)) != nil {
			return Rect{}, false
		}
		return Rect{X: b.Min.X, Y: b.Min.Y, W: b.Dx(), H: b.Dy(), PNG: buf.Bytes()}, true
	})
	if len(rects) > 0 {
		s.onFrame(rects, nil)
	}
}

// gfxError names the troubleshooting switch on graphics pipeline failures: the server sends no
// bitmap updates once the pipeline is open, so the session cannot go on without it.
func gfxError(err error) error {
	if errors.Is(err, rdpgfx.Err) {
		return fmt.Errorf("%w (set %s=1 to use the bitmap path)", err, noGFXEnv)
	}
	return err
}
