// Package rdpconn is an RDP client built on the vendored, MIT-licensed go-rdp (internal/rdpmit):
// connect over TLS or NLA, decode bitmap updates to RGBA, and send mouse/key input. The framebuffer
// rectangles are handed to a callback as PNG, to draw on a <canvas>
// (migrate-gui-to-go-wails phase 9 — RDP, framebuffer decoded in Go, no cgo).
package rdpconn

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"image"
	"image/png"
	"net"
	"runtime"
	"strconv"
	"sync"

	"rustxterm-go/internal/rdpmit/codec"
	"rustxterm-go/internal/rdpmit/logging"
	"rustxterm-go/internal/rdpmit/protocol/pdu"
	"rustxterm-go/internal/rdpmit/rdp"
)

func init() { logging.SetLevel(logging.LevelError) } // go-rdp logs every phase at INFO

// Protocol selects the RDP security negotiation.
type Protocol string

const (
	SSL Protocol = "ssl" // TLS + standard logon (xrdp, most servers)
	NLA Protocol = "nla" // CredSSP/NLA (modern Windows)
	// Std is "Allow legacy RDP security": the NLA offer (CredSSP or TLS) first; a server that refuses
	// TLS (Security Layer "RDP", SSL_NOT_ALLOWED_BY_SERVER) is retried with standard RDP security
	// (RC4, no TLS; trust is decided on the server's RSA key, VerifyLegacyKey). The other modes refuse standard security before any
	// credential is sent (rdp.ErrStandardSecurity).
	Std Protocol = "std"
)

// colorDepth is the requested session depth, as the Rust app: 32 bpp brings RDP 6 planar bitmaps
// (xrdp, 2008 R2) and lets RDP 8+ servers send NSCodec surface bits.
const colorDepth = 32

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
	// 2008 SChannel abort the first CredSSP record); use SSL for those.
	LegacyTLS bool
	// KeyboardLayout is the Windows input locale id sent to the server (0x0416 Portuguese (Brazil));
	// 0 lets the server pick its default, as the Rust app did.
	KeyboardLayout uint32
	// OnClipboard, when set, receives text copied on the remote desktop; nil leaves the clipboard
	// channel closed (no text crosses in either direction).
	OnClipboard func(string)
	// VerifyTLS decides whether the server certificate is trusted (tlstrust.Verifier); nil accepts
	// any certificate (tests only). It runs inside the TLS handshake, before any credential is sent.
	VerifyTLS func(tls.ConnectionState) error
	// VerifyLegacyKey decides trust on the server's RSA key fingerprint ("RSA AA:BB:…",
	// LegacyKeyPrefix) under standard RDP security (Std), before any credential is sent; nil accepts
	// any key (tests only).
	VerifyLegacyKey func(fingerprint string) error
}

// LegacyKeyPrefix starts a standard-security server key fingerprint.
const LegacyKeyPrefix = rdp.LegacyKeyPrefix

// Rect is a changed region as a PNG. onFrame gets all rectangles of one update together, in order,
// and the ids of surface frames that ended in it: the caller passes those to AckFrames once the
// rectangles are on screen, so a slow renderer throttles the server (MS-RDPRFX frame acknowledge).
type Rect struct {
	X, Y, W, H int
	PNG        []byte
}

// Session is an RDP connection: Connect prepares it, Start dials and logs in.
type Session struct {
	cfg     Config
	onFrame func([]Rect, []uint32)
	onReady func()
	onClose func(error)
	clip    Clip

	mu     sync.Mutex
	c      *rdp.Client
	closed bool
	once   sync.Once
}

// useNLA is the x224 offer: NLA and Std offer CredSSP or plain TLS (the server picks, as the Rust
// client does); SSL offers TLS only.
func useNLA(p Protocol) bool { return p == NLA || p == Std }

// tlsConfig leaves trust to VerifyTLS (pinning / trust on first use), not to CA roots.
func tlsConfig(cfg Config) *tls.Config {
	t := &tls.Config{
		InsecureSkipVerify: true, // #nosec G402 -- VerifyConnection decides trust
		VerifyConnection:   cfg.VerifyTLS,
		MinVersion:         tls.VersionTLS12,
		ServerName:         cfg.Host,
	}
	if cfg.LegacyTLS {
		t.MinVersion = tls.VersionTLS10 // opt-in for old servers; blocks NLA (see Config.LegacyTLS)
	}
	return t
}

// Connect prepares the session but does not dial or log in yet; call Start after the caller has
// subscribed to frame events. It returns the requested desktop size.
func Connect(cfg Config, onFrame func([]Rect, []uint32), onReady func(), onClose func(error)) (*Session, int, int, error) {
	if cfg.Width <= 0 {
		cfg.Width = 1024
	}
	if cfg.Height <= 0 {
		cfg.Height = 768
	}
	if cfg.Port == 0 {
		cfg.Port = 3389
	}
	s := &Session{cfg: cfg, onFrame: onFrame, onReady: onReady, onClose: onClose}
	s.clip.OnText = cfg.OnClipboard
	return s, cfg.Width, cfg.Height, nil
}

// Start dials, negotiates security, logs in and starts the update loop. On failure the session is
// closed and onClose receives the error too (the GUI shows its stopped screen from that event).
func (s *Session) Start() error {
	cfg := s.cfg
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	c, err := rdp.NewClient(addr, cfg.User, cfg.Password, cfg.Width, cfg.Height, colorDepth)
	if err != nil {
		s.finish(err)
		return err
	}
	c.SetUseNLA(useNLA(cfg.Protocol))
	c.SetAllowStandardSecurity(cfg.Protocol == Std)
	c.SetServerKeyCheck(cfg.VerifyLegacyKey)
	c.SetKeyboardLayout(cfg.KeyboardLayout)
	c.SetEnableRFX(true) // surface commands + NSCodec, offered to RDP 8+ servers only
	c.SetTLSClientConfig(tlsConfig(cfg))
	if gfxEnabled() {
		s.startGFX(c)
	}
	if cfg.OnClipboard != nil {
		s.clip.Send = func(b []byte) error { return c.SendStaticChannel(clipChannel, b) }
		c.AddStaticChannel(clipChannel, clipOptions, func(b []byte) error { s.clip.Receive(b); return nil })
	}

	s.mu.Lock()
	closed := s.closed
	s.c = c
	s.mu.Unlock()
	if closed { // Close won the race with the dial
		_ = c.Close()
		return net.ErrClosed
	}
	if err := c.Connect(); err != nil {
		err = gfxError(err)
		s.finish(err)
		return err
	}
	if s.onReady != nil {
		s.onReady()
	}
	go s.loop(c)
	return nil
}

func (s *Session) loop(c *rdp.Client) {
	var fp fastPath
	for {
		u, err := c.GetUpdate()
		if err != nil {
			s.finish(gfxError(err))
			return
		}
		var (
			rects  []Rect
			frames []uint32
		)
		b := s.budget()
		for _, up := range fp.updates(u.Data) {
			switch up.code {
			case fpUpdateBitmap:
				rects = append(rects, bitmapRects(up.data, b)...)
			case fpUpdateSurfCmds:
				jobs, ended := surfaceCommands(up.data, b)
				rects = append(rects, encodeJobs(jobs)...)
				frames = append(frames, ended...)
			}
		}
		if len(rects) > 0 || len(frames) > 0 {
			s.onFrame(rects, frames)
		}
	}
}

// finish closes the connection and reports once: nil when the caller closed it, err otherwise.
func (s *Session) finish(err error) {
	s.once.Do(func() {
		s.mu.Lock()
		c, closed := s.c, s.closed
		s.closed = true
		s.mu.Unlock()
		if c != nil {
			_ = c.Close()
		}
		if closed {
			err = nil
		}
		if s.onClose != nil {
			s.onClose(err)
		}
	})
}

// fastPath reassembles fast-path updates (MS-RDPBCGR 2.2.9.1.2.1); go-rdp hands slow-path bitmap
// updates over in the same form.
type fastPath struct{ frag []byte }

const (
	fpUpdateBitmap = 0x1
	fpFragSingle   = 0x0
	fpFragLast     = 0x1
	fpFragFirst    = 0x2
	fpCompressed   = 0x2  // FASTPATH_OUTPUT_COMPRESSION_USED
	packetCompr    = 0x20 // PACKET_COMPRESSED (bulk compression is never negotiated)
)

// fastUpdate is one complete (reassembled) fast-path update.
type fastUpdate struct {
	code byte
	data []byte
}

// updates returns the complete bitmap and surface-command updates in one fast-path PDU body.
func (f *fastPath) updates(data []byte) []fastUpdate {
	var out []fastUpdate
	for len(data) >= 3 {
		hdr, i, compr := data[0], 1, byte(0)
		if hdr>>6&fpCompressed != 0 {
			compr, i = data[1], 2
		}
		if len(data) < i+2 {
			break
		}
		size := int(binary.LittleEndian.Uint16(data[i:]))
		i += 2
		if len(data) < i+size {
			break
		}
		body := data[i : i+size]
		data = data[i+size:]
		if compr&packetCompr != 0 {
			f.frag = nil
			continue
		}
		var update []byte
		switch hdr >> 4 & 0x3 {
		case fpFragSingle:
			update = body
		case fpFragFirst:
			f.frag = append(f.frag[:0], body...)
			continue
		case fpFragLast:
			update, f.frag = append(f.frag, body...), nil
		default: // FASTPATH_FRAGMENT_NEXT
			f.frag = append(f.frag, body...)
			continue
		}
		if code := hdr & 0xf; code == fpUpdateBitmap || code == fpUpdateSurfCmds {
			out = append(out, fastUpdate{code, update})
		}
	}
	return out
}

const (
	bitmapCompression = 0x0001 // BITMAP_COMPRESSION
	noCompressionHdr  = 0x0400 // NO_BITMAP_COMPRESSION_HDR
)

// AckFrames acknowledges surface frames once they are drawn; RDP 8+ servers stop sending after a
// few unacknowledged frames.
func (s *Session) AckFrames(ids []uint32) {
	s.mu.Lock()
	c := s.c
	s.mu.Unlock()
	if c == nil {
		return
	}
	for _, id := range ids {
		_ = c.SendFrameAcknowledge(id)
	}
}

// pixelBudget bounds what one update may make this client decode: each rectangle at most a desktop
// (plus a tile of padding) and all of them together at most four desktops. Server sizes are
// untrusted: a 65535×65535 rectangle would otherwise allocate gigabytes.
type pixelBudget struct{ rect, left int }

func (s *Session) budget() *pixelBudget {
	px := (s.cfg.Width + 64) * (s.cfg.Height + 64)
	return &pixelBudget{rect: px, left: 4 * px}
}

// take reserves a w×h rectangle; false means skip it.
func (b *pixelBudget) take(w, h int) bool {
	if w <= 0 || h <= 0 || w > maxSide || h > maxSide || w*h > b.rect || w*h > b.left {
		return false
	}
	b.left -= w * h
	return true
}

// maxSide is the largest rectangle side accepted (MS-RDPBCGR desktops are at most 8192 wide).
const maxSide = 8192

// bitmapRect is one rectangle of a bitmap update, still encoded.
type bitmapRect struct {
	data                                []byte
	left, top, right, bottom, w, h, bpp int
	compressed, noHdr                   bool
}

// bitmapRects decodes a TS_UPDATE_BITMAP_DATA (MS-RDPBCGR 2.2.9.1.1.3.1.2) into PNG rectangles, in
// update order. The rectangles are decoded and PNG-encoded in parallel: done one by one on the
// receive goroutine, a full-screen redraw (hundreds of 64×64 tiles) held up reading the socket.
func bitmapRects(p []byte, b *pixelBudget) []Rect {
	if len(p) < 4 || binary.LittleEndian.Uint16(p) != 1 { // UPDATETYPE_BITMAP
		return nil
	}
	n := int(binary.LittleEndian.Uint16(p[2:]))
	p = p[4:]
	var jobs []bitmapRect
	for ; n > 0 && len(p) >= 18; n-- {
		v := func(i int) int { return int(binary.LittleEndian.Uint16(p[2*i:])) }
		left, top, right, bottom, w, h, bpp, flags, length := v(0), v(1), v(2), v(3), v(4), v(5), v(6), v(7), v(8)
		p = p[18:]
		if len(p) < length {
			break
		}
		data := p[:length]
		p = p[length:]
		compressed := flags&bitmapCompression != 0
		noHdr := flags&noCompressionHdr != 0
		if compressed && !noHdr { // skip TS_CD_HEADER
			if len(data) < 8 {
				continue
			}
			data = data[8:]
		}
		if !b.take(w, h) {
			continue
		}
		// a compressed 32 bpp bitmap is always RDP 6 planar (MS-RDPBCGR 2.2.9.1.1.3.1.2.2), with or
		// without the TS_CD_HEADER stripped above; ProcessBitmap picks planar from noHdr
		jobs = append(jobs, bitmapRect{data, left, top, right, bottom, w, h, bpp, compressed, noHdr || (compressed && bpp == 32)})
	}
	return parallelRects(len(jobs), func(i int) (Rect, bool) {
		j := jobs[i]
		rgba := codec.ProcessBitmap(j.data, j.w, j.h, j.bpp, j.compressed, 0, j.noHdr)
		return encode(rgba, j.w, j.h, j.left, j.top, j.right, j.bottom)
	})
}

// encodeJobs PNG-encodes decoded surface tiles in parallel, in order.
func encodeJobs(jobs []tileJob) []Rect {
	return parallelRects(len(jobs), func(i int) (Rect, bool) {
		j := jobs[i]
		return encode(j.rgba, j.w, j.h, j.x, j.y, j.x+j.w-1, j.y+j.h-1)
	})
}

// parallelRects runs f for 0..n-1 on up to GOMAXPROCS goroutines and keeps the results in order.
func parallelRects(n int, f func(int) (Rect, bool)) []Rect {
	rects, ok := make([]Rect, n), make([]bool, n)
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer func() { <-sem; wg.Done() }()
			rects[i], ok[i] = f(i)
		}(i)
	}
	wg.Wait()
	out := rects[:0]
	for i, r := range rects {
		if ok[i] {
			out = append(out, r)
		}
	}
	return out
}

// encode turns a decoded top-down w×h RGBA bitmap into a PNG at its screen position, cropped to
// left..right × top..bottom (inclusive): the bitmap is padded (width to a multiple of 4) and drawing
// the padding columns too left black vertical lines wherever the neighbouring area was not redrawn.
func encode(rgba []byte, w, h, left, top, right, bottom int) (Rect, bool) {
	if w <= 0 || h <= 0 || len(rgba) < w*h*4 {
		return Rect{}, false
	}
	vw, vh := right-left+1, bottom-top+1
	if vw <= 0 || vw > w {
		vw = w
	}
	if vh <= 0 || vh > h {
		vh = h
	}
	img := (&image.RGBA{Pix: rgba, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}).SubImage(image.Rect(0, 0, vw, vh))
	var buf bytes.Buffer
	if (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img) != nil {
		return Rect{}, false
	}
	return Rect{X: left, Y: top, W: vw, H: vh, PNG: buf.Bytes()}, true
}

// buttonFlag maps 0 left, 1 middle, 2 right to TS_POINTER_EVENT BUTTON1 / BUTTON3 / BUTTON2.
func buttonFlag(button int) uint16 {
	switch button {
	case 1:
		return pdu.PTRFlagsButton3
	case 2:
		return pdu.PTRFlagsButton2
	default:
		return pdu.PTRFlagsButton1
	}
}

func (s *Session) client() *rdp.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	return s.c
}

func (s *Session) input(ev *pdu.InputEvent) {
	if c := s.client(); c != nil {
		_ = c.SendInputEvent(ev.Serialize())
	}
}

func (s *Session) mouse(flags uint16, x, y int) {
	s.input(pdu.NewMouseEvent(flags, uint16(max(x, 0)), uint16(max(y, 0))))
}

// MouseMove / MouseDown / MouseUp / Wheel / KeyDown / KeyUp drive input. button: 0 left, 1 middle,
// 2 right.
func (s *Session) MouseMove(x, y int)         { s.mouse(pdu.PTRFlagsMove, x, y) }
func (s *Session) MouseDown(button, x, y int) { s.mouse(pdu.PTRFlagsDown|buttonFlag(button), x, y) }
func (s *Session) MouseUp(button, x, y int)   { s.mouse(buttonFlag(button), x, y) }

// Wheel sends a vertical rotation (±120 per notch) as a 9-bit two's complement, whose sign bit is
// PTRFLAGS_WHEEL_NEGATIVE.
func (s *Session) Wheel(scroll, x, y int) { s.mouse(wheelFlags(scroll), x, y) }

func wheelFlags(scroll int) uint16 { return pdu.PTRFlagsWheel | uint16(scroll)&0x1ff }

// KeyDown / KeyUp send a PC set-1 scancode.
func (s *Session) KeyDown(scancode int) { s.input(pdu.NewKeyboardEvent(0, uint8(scancode))) }
func (s *Session) KeyUp(scancode int) {
	s.input(pdu.NewKeyboardEvent(pdu.KBDFlagsRelease, uint8(scancode)))
}

// SetClipboard offers local clipboard text to the remote desktop.
func (s *Session) SetClipboard(text string) error { return s.clip.SetText(text) }

// Close ends the session; onClose then receives nil.
func (s *Session) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.finish(nil)
	return nil
}
