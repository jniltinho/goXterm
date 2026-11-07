// Package vncconn is a minimal pure-Go VNC/RFB client: RFB 3.8 handshake, VncAuth (DES
// challenge-response), Raw and CopyRect encodings, and pointer/key input. The framebuffer is kept as
// an image.RGBA; changed rectangles are handed to a callback as PNG, to draw on a <canvas>
// (migrate-gui-to-go-wails phase 8 — VNC, framebuffer decoded in Go). No cgo.
package vncconn

import (
	"bufio"
	"bytes"
	"crypto/des" //nolint:gosec // VncAuth is defined in terms of DES; required for protocol compatibility
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"sync"
	"time"
)

// Rect is a changed region handed to onFrame as a PNG of the current framebuffer.
type Rect struct {
	X, Y, W, H int
	PNG        []byte
}

// Config describes one VNC connection.
type Config struct {
	Host     string
	Port     int
	Password string
}

// Session is an open VNC connection.
type Session struct {
	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer

	mu     sync.Mutex
	fb     *image.RGBA
	width  int
	height int
	closed bool
}

const (
	encRaw         = 0
	encCopyRect    = 1
	encDesktopSize = -223
)

// Connect performs the handshake and starts the update loop, calling onInit once with the desktop
// size and name, onFrame for each changed rectangle, and onClose when the loop ends.
func Connect(cfg Config, onInit func(w, h int, name string), onFrame func(Rect), onClose func(error)) (*Session, error) {
	addr := net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port))
	c, err := net.DialTimeout("tcp", addr, 15*time.Second)
	if err != nil {
		return nil, err
	}
	s := &Session{conn: c, r: bufio.NewReaderSize(c, 64*1024), w: bufio.NewWriter(c)}
	width, height, name, err := s.handshake(cfg.Password)
	if err != nil {
		c.Close()
		return nil, err
	}
	s.width, s.height = width, height
	s.fb = image.NewRGBA(image.Rect(0, 0, width, height))
	if onInit != nil {
		onInit(width, height, name)
	}
	// The read loop starts now but blocks until Start() requests the first update, so the caller can
	// subscribe to frame events before any arrive.
	go s.loop(onFrame, onClose)
	return s, nil
}

// Start requests the first (full) framebuffer update; call it after subscribing to frame events.
func (s *Session) Start() error { return s.requestUpdate(false) }

func (s *Session) handshake(password string) (int, int, string, error) {
	// ProtocolVersion
	ver := make([]byte, 12)
	if _, err := io.ReadFull(s.r, ver); err != nil {
		return 0, 0, "", err
	}
	if _, err := s.w.Write([]byte("RFB 003.008\n")); err != nil {
		return 0, 0, "", err
	}
	if err := s.w.Flush(); err != nil {
		return 0, 0, "", err
	}
	// Security types
	n, err := s.r.ReadByte()
	if err != nil {
		return 0, 0, "", err
	}
	if n == 0 {
		return 0, 0, "", fmt.Errorf("vnc: server refused connection")
	}
	types := make([]byte, n)
	if _, err := io.ReadFull(s.r, types); err != nil {
		return 0, 0, "", err
	}
	if !bytes.Contains(types, []byte{2}) { // VncAuth
		return 0, 0, "", fmt.Errorf("vnc: server offers no VncAuth (types %v)", types)
	}
	if _, err := s.w.Write([]byte{2}); err != nil {
		return 0, 0, "", err
	}
	if err := s.w.Flush(); err != nil {
		return 0, 0, "", err
	}
	// VncAuth challenge-response
	challenge := make([]byte, 16)
	if _, err := io.ReadFull(s.r, challenge); err != nil {
		return 0, 0, "", err
	}
	resp, err := vncAuthResponse(password, challenge)
	if err != nil {
		return 0, 0, "", err
	}
	if _, err := s.w.Write(resp); err != nil {
		return 0, 0, "", err
	}
	if err := s.w.Flush(); err != nil {
		return 0, 0, "", err
	}
	var result uint32
	if err := binary.Read(s.r, binary.BigEndian, &result); err != nil {
		return 0, 0, "", err
	}
	if result != 0 {
		return 0, 0, "", fmt.Errorf("vnc: authentication failed (wrong password)")
	}
	// ClientInit (shared)
	if _, err := s.w.Write([]byte{1}); err != nil {
		return 0, 0, "", err
	}
	if err := s.w.Flush(); err != nil {
		return 0, 0, "", err
	}
	// ServerInit
	var si struct {
		Width, Height uint16
		PF            [16]byte
		NameLen       uint32
	}
	if err := binary.Read(s.r, binary.BigEndian, &si); err != nil {
		return 0, 0, "", err
	}
	nameBuf := make([]byte, si.NameLen)
	if _, err := io.ReadFull(s.r, nameBuf); err != nil {
		return 0, 0, "", err
	}
	if err := s.setPixelFormat(); err != nil {
		return 0, 0, "", err
	}
	if err := s.setEncodings([]int32{encCopyRect, encRaw, encDesktopSize}); err != nil {
		return 0, 0, "", err
	}
	return int(si.Width), int(si.Height), string(nameBuf), nil
}

// setPixelFormat asks for 32bpp true-color little-endian with R<<16,G<<8,B so a pixel's 4 bytes are
// B,G,R,x — simple to turn into RGBA.
func (s *Session) setPixelFormat() error {
	msg := []byte{
		0, 0, 0, 0, // type + padding
		32, 24, 0, 1, // bpp, depth, big-endian=0, true-color=1
		0, 255, 0, 255, 0, 255, // red/green/blue max (u16 BE)
		16, 8, 0, // shifts
		0, 0, 0, // padding
	}
	if _, err := s.w.Write(msg); err != nil {
		return err
	}
	return s.w.Flush()
}

func (s *Session) setEncodings(encs []int32) error {
	buf := new(bytes.Buffer)
	buf.Write([]byte{2, 0}) // type + padding
	binary.Write(buf, binary.BigEndian, uint16(len(encs)))
	for _, e := range encs {
		binary.Write(buf, binary.BigEndian, e)
	}
	if _, err := s.w.Write(buf.Bytes()); err != nil {
		return err
	}
	return s.w.Flush()
}

func (s *Session) requestUpdate(incremental bool) error {
	s.mu.Lock()
	w, h := s.width, s.height
	s.mu.Unlock()
	inc := byte(0)
	if incremental {
		inc = 1
	}
	buf := new(bytes.Buffer)
	buf.WriteByte(3) // FramebufferUpdateRequest
	buf.WriteByte(inc)
	binary.Write(buf, binary.BigEndian, uint16(0))
	binary.Write(buf, binary.BigEndian, uint16(0))
	binary.Write(buf, binary.BigEndian, uint16(w))
	binary.Write(buf, binary.BigEndian, uint16(h))
	if _, err := s.w.Write(buf.Bytes()); err != nil {
		return err
	}
	return s.w.Flush()
}

func (s *Session) loop(onFrame func(Rect), onClose func(error)) {
	var err error
	for {
		var msgType byte
		msgType, err = s.r.ReadByte()
		if err != nil {
			break
		}
		switch msgType {
		case 0: // FramebufferUpdate
			if err = s.readFramebufferUpdate(onFrame); err != nil {
				goto done
			}
			if err = s.requestUpdate(true); err != nil {
				goto done
			}
		case 1: // SetColourMapEntries
			err = s.skipColourMap()
		case 2: // Bell
		case 3: // ServerCutText
			err = s.skipServerCutText()
		default:
			err = fmt.Errorf("vnc: unknown server message %d", msgType)
		}
		if err != nil {
			break
		}
	}
done:
	s.markClosed()
	if onClose != nil {
		onClose(err)
	}
}

func (s *Session) readFramebufferUpdate(onFrame func(Rect)) error {
	if _, err := s.r.Discard(1); err != nil { // padding
		return err
	}
	var numRects uint16
	if err := binary.Read(s.r, binary.BigEndian, &numRects); err != nil {
		return err
	}
	for i := 0; i < int(numRects); i++ {
		var h struct {
			X, Y, W, H uint16
			Enc        int32
		}
		if err := binary.Read(s.r, binary.BigEndian, &h); err != nil {
			return err
		}
		switch h.Enc {
		case encRaw:
			if err := s.readRaw(int(h.X), int(h.Y), int(h.W), int(h.H)); err != nil {
				return err
			}
		case encCopyRect:
			if err := s.readCopyRect(int(h.X), int(h.Y), int(h.W), int(h.H)); err != nil {
				return err
			}
		case encDesktopSize:
			s.resize(int(h.W), int(h.H))
			continue // no pixel data; whole screen will be resent
		default:
			return fmt.Errorf("vnc: unexpected encoding %d (only Raw/CopyRect negotiated)", h.Enc)
		}
		if onFrame != nil && h.W > 0 && h.H > 0 {
			if r, ok := s.encodeRect(int(h.X), int(h.Y), int(h.W), int(h.H)); ok {
				onFrame(r)
			}
		}
	}
	return nil
}

func (s *Session) readRaw(x, y, w, h int) error {
	pix := make([]byte, w*h*4)
	if _, err := io.ReadFull(s.r, pix); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			i := (row*w + col) * 4
			b, g, r := pix[i], pix[i+1], pix[i+2] // B,G,R,x
			s.fb.SetRGBA(x+col, y+row, rgba(r, g, b))
		}
	}
	return nil
}

func (s *Session) readCopyRect(x, y, w, h int) error {
	var src struct{ X, Y uint16 }
	if err := binary.Read(s.r, binary.BigEndian, &src); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// copy via a temp to handle overlap
	tmp := image.NewRGBA(image.Rect(0, 0, w, h))
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			tmp.SetRGBA(col, row, s.fb.RGBAAt(int(src.X)+col, int(src.Y)+row))
		}
	}
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			s.fb.SetRGBA(x+col, y+row, tmp.RGBAAt(col, row))
		}
	}
	return nil
}

func (s *Session) encodeRect(x, y, w, h int) (Rect, bool) {
	s.mu.Lock()
	sub := s.fb.SubImage(image.Rect(x, y, x+w, y+h))
	s.mu.Unlock()
	var buf bytes.Buffer
	if err := png.Encode(&buf, sub); err != nil {
		return Rect{}, false
	}
	return Rect{X: x, Y: y, W: w, H: h, PNG: buf.Bytes()}, true
}

func (s *Session) resize(w, h int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.width, s.height = w, h
	s.fb = image.NewRGBA(image.Rect(0, 0, w, h))
}

func (s *Session) skipColourMap() error {
	if _, err := s.r.Discard(3); err != nil {
		return err
	}
	var n uint16
	if err := binary.Read(s.r, binary.BigEndian, &n); err != nil {
		return err
	}
	_, err := s.r.Discard(int(n) * 6)
	return err
}

func (s *Session) skipServerCutText() error {
	if _, err := s.r.Discard(3); err != nil {
		return err
	}
	var n uint32
	if err := binary.Read(s.r, binary.BigEndian, &n); err != nil {
		return err
	}
	_, err := s.r.Discard(int(n))
	return err
}

// Pointer sends a pointer event (buttonMask bit0=left,1=middle,2=right,3=up,4=down).
func (s *Session) Pointer(buttonMask int, x, y int) error {
	buf := []byte{5, byte(buttonMask), byte(x >> 8), byte(x), byte(y >> 8), byte(y)}
	return s.send(buf)
}

// Key sends a key event (down true = press).
func (s *Session) Key(down bool, keysym uint32) error {
	d := byte(0)
	if down {
		d = 1
	}
	buf := []byte{4, d, 0, 0, byte(keysym >> 24), byte(keysym >> 16), byte(keysym >> 8), byte(keysym)}
	return s.send(buf)
}

func (s *Session) send(b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("vnc session closed")
	}
	if _, err := s.w.Write(b); err != nil {
		return err
	}
	return s.w.Flush()
}

// Close ends the session.
func (s *Session) Close() error {
	s.markClosed()
	return s.conn.Close()
}

func (s *Session) markClosed() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func rgba(r, g, b byte) color.RGBA { return color.RGBA{R: r, G: g, B: b, A: 255} }

// vncAuthResponse computes the 16-byte VncAuth response: DES-encrypt the challenge in two blocks
// with the password as the key, each key byte's bits reversed (the VNC quirk).
func vncAuthResponse(password string, challenge []byte) ([]byte, error) {
	key := make([]byte, 8)
	copy(key, []byte(password))
	for i := range key {
		key[i] = reverseBits(key[i])
	}
	block, err := des.NewCipher(key)
	if err != nil {
		return nil, err
	}
	resp := make([]byte, 16)
	block.Encrypt(resp[0:8], challenge[0:8])
	block.Encrypt(resp[8:16], challenge[8:16])
	return resp, nil
}

func reverseBits(b byte) byte {
	var r byte
	for i := 0; i < 8; i++ {
		r = (r << 1) | (b & 1)
		b >>= 1
	}
	return r
}
