// Package rdpconn is an RDP client built on the vendored grdp (internal/grdp, a Linux-building fork
// with the bitmap-event fix): connect, decode bitmap updates to RGBA, and send mouse/key input. The
// framebuffer rectangles are handed to a callback as PNG, to draw on a <canvas>
// (migrate-gui-to-go-wails phase 9 — RDP, framebuffer decoded in Go, no cgo).
package rdpconn

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"sync"

	"rustxterm-go/internal/grdp/client"
	"rustxterm-go/internal/grdp/glog"
	"rustxterm-go/internal/grdp/protocol/x224"
)

func init() { glog.SetLevel(glog.ERROR) } // grdp is chatty; keep its logging quiet

// Protocol selects the RDP security negotiation.
type Protocol string

const (
	SSL Protocol = "ssl" // TLS + standard logon (xrdp, most servers)
	NLA Protocol = "nla" // CredSSP/NLA (modern Windows)
	Std Protocol = "std" // legacy standard RDP security
)

// Config describes one RDP connection.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Width    int
	Height   int
	Protocol Protocol
	// LegacyTLS allows TLS 1.0 for old servers (Windows 2008). Default is TLS 1.2+. NLA over TLS 1.0
	// cannot work on Go's crypto/tls (its 1/n-1 CBC record split, which cannot be disabled, makes
	// 2008 SChannel abort the first CredSSP record); use Std/legacy RDP security for those, or SSL.
	LegacyTLS bool
	// OnClipboard, when set, receives text copied on the remote desktop.
	OnClipboard func(string)
	// VerifyTLS decides whether the server certificate is trusted (tlstrust.Verifier); nil accepts
	// any certificate (tests only).
	VerifyTLS func(tls.ConnectionState) error
}

// Rect is a changed region handed to onFrame as a PNG.
type Rect struct {
	X, Y, W, H int
	PNG        []byte
}

// Session is an open RDP connection.
type Session struct {
	c      *client.Client
	mu     sync.Mutex
	closed bool
}

// requested is the x224 protocol offer: NLA offers CredSSP or plain TLS (the server picks, as the
// Rust client does), SSL offers TLS, Std (only with "Allow legacy RDP security") offers all three
// and is the only mode that accepts the unencrypted standard RDP security (see AllowStandardSecurity).
func requested(p Protocol) uint32 {
	switch p {
	case NLA:
		return x224.PROTOCOL_HYBRID | x224.PROTOCOL_SSL
	case Std:
		return x224.PROTOCOL_HYBRID | x224.PROTOCOL_SSL // | PROTOCOL_RDP, which is 0
	default:
		return x224.PROTOCOL_SSL
	}
}

// Connect builds the client and registers handlers but does not log in yet; call Start after the
// caller has subscribed to frame events. width/height are the requested desktop size.
func Connect(cfg Config, onFrame func(Rect), onReady func(), onClose func(error)) (*Session, int, int, error) {
	w, h := cfg.Width, cfg.Height
	if w <= 0 {
		w = 1024
	}
	if h <= 0 {
		h = 768
	}
	port := cfg.Port
	if port == 0 {
		port = 3389
	}
	s := client.NewSetting()
	s.Width, s.Height = w, h
	s.LogLevel = glog.ERROR
	s.SetRequestedProtocol(requested(cfg.Protocol))
	s.AllowStandardSecurity = cfg.Protocol == Std
	s.TLSVerify = false // trust is decided by VerifyTLS (pinning / trust on first use), not by CA roots alone
	s.VerifyConnection = cfg.VerifyTLS
	s.TLSMinVersion = tls.VersionTLS12
	if cfg.LegacyTLS {
		s.TLSMinVersion = tls.VersionTLS10 // opt-in for old servers; blocks NLA (see Config.LegacyTLS)
	}

	c := client.NewClient(fmt.Sprintf("%s:%d", cfg.Host, port), cfg.User, cfg.Password, client.TC_RDP, s)
	sess := &Session{c: c}

	c.OnError(func(e error) { sess.markClosed(); onClose(e) })
	c.OnClose(func() { sess.markClosed(); onClose(nil) })
	if onReady != nil {
		c.OnReady(onReady)
	}
	if cfg.OnClipboard != nil {
		c.OnClipboard(cfg.OnClipboard)
	}
	c.OnBitmap(func(bs []client.Bitmap) {
		for i := range bs {
			if r, ok := encode(bs[i]); ok {
				onFrame(r)
			}
		}
	})
	return sess, w, h, nil
}

// Start logs in and begins the update flow (blocks until the RDP sequence connects).
func (s *Session) Start() error {
	return s.c.LoginContext(context.Background())
}

// visible is the on-screen size of a bitmap update: its bitmap is padded (width to a multiple of 4)
// and only DestLeft..DestRight × DestTop..DestBottom (inclusive) belongs on screen.
func visible(b client.Bitmap) (int, int) {
	w, h := b.DestRight-b.DestLeft+1, b.DestBottom-b.DestTop+1
	if w <= 0 || w > b.Width {
		w = b.Width
	}
	if h <= 0 || h > b.Height {
		h = b.Height
	}
	return w, h
}

// encode converts one decoded bitmap (top-down, 16/24/32 bpp) to an RGBA PNG at its screen position,
// cropped to its visible rectangle — drawing the padding columns too left black vertical lines
// wherever the neighbouring area was not redrawn (partial updates: hover, closing a menu).
func encode(b client.Bitmap) (Rect, bool) {
	if b.Width <= 0 || b.Height <= 0 || len(b.Data) == 0 {
		return Rect{}, false
	}
	bpp := b.BitsPerPixel // bytes per pixel (grdp Bpp = bits/8)
	vw, vh := visible(b)
	img := image.NewRGBA(image.Rect(0, 0, vw, vh))
	for y := 0; y < vh; y++ {
		// grdp's RLE decoder already yields top-down rows, so each rectangle is read in order (an extra
		// bottom-up flip turned every tile upside down against real servers — 2008 R2).
		src := y
		for x := 0; x < vw; x++ {
			i := (src*b.Width + x) * bpp
			if i+bpp > len(b.Data) {
				continue
			}
			var r, g, bl byte
			switch bpp {
			case 2:
				p := uint16(b.Data[i]) | uint16(b.Data[i+1])<<8 // RGB565 little-endian
				r = byte((p >> 11 & 0x1f) << 3)
				g = byte((p >> 5 & 0x3f) << 2)
				bl = byte((p & 0x1f) << 3)
			case 3, 4:
				bl, g, r = b.Data[i], b.Data[i+1], b.Data[i+2] // BGR(A)
			default:
				return Rect{}, false
			}
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: bl, A: 255})
		}
	}
	var buf bytes.Buffer
	if png.Encode(&buf, img) != nil {
		return Rect{}, false
	}
	return Rect{X: b.DestLeft, Y: b.DestTop, W: vw, H: vh, PNG: buf.Bytes()}, true
}

// MouseMove / MouseButton / Wheel / Key drive input. button: 0 left, 1 middle, 2 right.
func (s *Session) MouseMove(x, y int)         { s.c.MouseMove(x, y) }
func (s *Session) MouseDown(button, x, y int) { s.c.MouseDown(button, x, y) }
func (s *Session) MouseUp(button, x, y int)   { s.c.MouseUp(button, x, y) }
func (s *Session) Wheel(scroll, x, y int)     { s.c.MouseWheel(scroll, x, y) }
func (s *Session) KeyDown(scancode int)       { s.c.KeyDown(scancode, "") }
func (s *Session) KeyUp(scancode int)         { s.c.KeyUp(scancode, "") }

// SetClipboard offers local clipboard text to the remote desktop.
func (s *Session) SetClipboard(text string) error { return s.c.SetClipboard(text) }

// Close ends the session.
func (s *Session) Close() error {
	s.markClosed()
	s.c.Close()
	return nil
}

func (s *Session) markClosed() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}
