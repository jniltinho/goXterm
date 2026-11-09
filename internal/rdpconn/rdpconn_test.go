package rdpconn

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"image/png"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"rustxterm-go/internal/rdpmit/rdp"
)

// labConfig is the protocol lab's xrdp (alice / lab-alice-pass on 127.0.0.1:13389). Lab credentials
// are fictional.
func labConfig(t *testing.T) Config {
	if os.Getenv("RXT_SSH_LAB") == "" {
		t.Skip("set RXT_SSH_LAB=1 with the lab rdp up (127.0.0.1:13389)")
	}
	return Config{
		Host: "127.0.0.1", Port: 13389, User: "alice", Password: "lab-alice-pass",
		Width: 1024, Height: 768, Protocol: SSL,
	}
}

// dial connects with cfg and returns the session plus its first-frame and close channels.
func dial(t *testing.T, cfg Config) (*Session, chan Rect, chan error) {
	frame := make(chan Rect, 1)
	closed := make(chan error, 1)
	var s *Session
	s, w, h, err := Connect(cfg, func(rs []Rect, frames []uint32) {
		s.AckFrames(frames)
		if len(rs) == 0 {
			return
		}
		select {
		case frame <- rs[0]:
		default:
		}
	}, nil, func(e error) {
		select {
		case closed <- e:
		default:
		}
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Logf("requested desktop %dx%d (proto %s)", w, h, cfg.Protocol)
	return s, frame, closed
}

func waitFrame(t *testing.T, frame chan Rect, closed chan error, d time.Duration) {
	t.Helper()
	select {
	case r := <-frame:
		if len(r.PNG) == 0 {
			t.Fatal("empty frame PNG")
		}
		t.Logf("frame %dx%d at %d,%d, %d-byte png", r.W, r.H, r.X, r.Y, len(r.PNG))
	case e := <-closed:
		t.Fatalf("closed before a frame: %v", e)
	case <-time.After(d):
		t.Fatal("no bitmap update in time")
	}
}

// TestLabRDP connects to the lab xrdp over TLS and checks that the certificate check runs inside the
// handshake, a bitmap rectangle decodes to a PNG, and the clipboard channel comes up (the server's
// Monitor Ready reached the client). Needs the lab up; skipped unless RXT_SSH_LAB=1.
func TestLabRDP(t *testing.T) {
	cfg := labConfig(t)
	cfg.OnClipboard = func(string) {} // opens the cliprdr channel (checked below)
	verified := false
	cfg.VerifyTLS = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("no server certificate")
		}
		verified = true
		return nil
	}
	s, frame, closed := dial(t, cfg)
	defer s.Close()
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !verified {
		t.Fatal("VerifyTLS was not called during the handshake")
	}
	waitFrame(t, frame, closed, 20*time.Second)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		s.clip.mu.Lock()
		ready := s.clip.ready
		s.clip.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cliprdr: no Monitor Ready from the server")
		}
	}
}

// TestLabRDPRejectedCertificate: when VerifyTLS refuses the certificate the login fails (no
// credential travels: the handshake itself aborts) and onClose reports the error.
func TestLabRDPRejectedCertificate(t *testing.T) {
	cfg := labConfig(t)
	refused := errors.New("certificate not trusted")
	cfg.VerifyTLS = func(tls.ConnectionState) error { return refused }
	s, _, closed := dial(t, cfg)
	defer s.Close()
	if err := s.Start(); !errors.Is(err, refused) {
		t.Fatalf("start = %v, want the VerifyTLS error", err)
	}
	if e := <-closed; !errors.Is(e, refused) {
		t.Fatalf("onClose got %v", e)
	}
}

// TestLabRDPNLAOffer: the NLA offer (CredSSP or TLS) also logs in against xrdp, which picks TLS.
func TestLabRDPNLAOffer(t *testing.T) {
	cfg := labConfig(t)
	cfg.Protocol = NLA
	s, frame, closed := dial(t, cfg)
	defer s.Close()
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFrame(t, frame, closed, 20*time.Second)
}

// TestLabRDPStandardSecurity logs in to the lab's rdp-std (xrdp with security_layer=rdp, port 13390):
// xrdp answers the TLS offer by selecting standard RDP security (Windows refuses it with
// SSL_NOT_ALLOWED_BY_SERVER instead, see rdp.TestStandardSecurityNotAllowed); Std goes on with
// 128-bit RC4 and the encrypted updates decode, the NLA mode refuses before any credential is sent.
func TestLabRDPStandardSecurity(t *testing.T) {
	cfg := labConfig(t)
	cfg.Port, cfg.Protocol = 13390, Std
	cfg.VerifyTLS = func(tls.ConnectionState) error { return errors.New("no TLS expected") }
	s, frame, closed := dial(t, cfg)
	defer s.Close()
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFrame(t, frame, closed, 20*time.Second)
	for i := range 20 { // encrypted fast-path input: the server keeps the session
		s.MouseMove(100+i, 100)
	}
	select {
	case e := <-closed:
		t.Fatalf("closed after input: %v", e)
	case <-time.After(2 * time.Second):
	}

	cfg.Protocol = NLA
	s2, _, _ := dial(t, cfg)
	defer s2.Close()
	if err := s2.Start(); !errors.Is(err, rdp.ErrStandardSecurity) {
		t.Fatalf("NLA mode: %v, want rdp.ErrStandardSecurity", err)
	}
}

// TestLabRDPClipboard checks both clipboard directions against the lab xrdp. The remote side is
// driven from the host while it runs (the test container has no docker access):
//
//	docker exec -u alice -e DISPLAY=:11 rxt-lab-rdp-1 xclip -o -selection clipboard   # → rxt-local-text
//	docker exec -u alice -e DISPLAY=:11 rxt-lab-rdp-1 sh -c 'printf rxt-remote-text | xclip -i -selection clipboard'
//
// Skipped unless RXT_RDP_CLIP=1 (and RXT_SSH_LAB=1).
func TestLabRDPClipboard(t *testing.T) {
	cfg := labConfig(t)
	if os.Getenv("RXT_RDP_CLIP") == "" {
		t.Skip("set RXT_RDP_CLIP=1 and drive xclip in the lab container (see the comment)")
	}
	got := make(chan string, 4)
	cfg.OnClipboard = func(s string) { got <- s }
	s, frame, closed := dial(t, cfg)
	defer s.Close()
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFrame(t, frame, closed, 20*time.Second)
	time.Sleep(2 * time.Second) // Monitor Ready
	if err := s.SetClipboard("rxt-local-text"); err != nil {
		t.Fatal(err)
	}
	t.Log("local text offered; waiting for remote text")
	for {
		select {
		case text := <-got:
			t.Logf("remote clipboard: %q", text)
			if text == "rxt-remote-text" {
				return
			}
		case e := <-closed:
			t.Fatalf("closed: %v", e)
		case <-time.After(90 * time.Second):
			t.Fatal("no remote clipboard text in time")
		}
	}
}

// TestRealRDP connects to an arbitrary RDP server from the environment and captures one frame
// (connectivity/decode check only — no input is sent, nothing on the server is changed). All
// connection details, including the password, come from env so no credentials live in the source.
// Skipped unless RXT_RDP_REAL=1. Env: RXT_RDP_HOST, RXT_RDP_PORT (default 3389), RXT_RDP_USER,
// RXT_RDP_PASS, RXT_RDP_PROTO (ssl|nla|std, default nla), RXT_RDP_LAYOUT (keyboard layout, e.g. 0x0416),
// RXT_RDP_W / RXT_RDP_H (desktop size, default 1024x768), RXT_RDP_HOLD (stay connected, e.g. 15s).
func TestRealRDP(t *testing.T) {
	if os.Getenv("RXT_RDP_REAL") == "" {
		t.Skip("set RXT_RDP_REAL=1 with RXT_RDP_HOST/USER/PASS to run")
	}
	port, _ := strconv.Atoi(os.Getenv("RXT_RDP_PORT"))
	layout, _ := strconv.ParseUint(os.Getenv("RXT_RDP_LAYOUT"), 0, 32)
	proto := Protocol(os.Getenv("RXT_RDP_PROTO"))
	if proto == "" {
		proto = NLA
	}
	cfg := Config{
		Host: os.Getenv("RXT_RDP_HOST"), Port: port,
		User: os.Getenv("RXT_RDP_USER"), Password: os.Getenv("RXT_RDP_PASS"),
		Width: envInt("RXT_RDP_W", 1024), Height: envInt("RXT_RDP_H", 768), Protocol: proto,
		LegacyTLS: os.Getenv("RXT_RDP_LEGACY") != "", KeyboardLayout: uint32(layout),
	}
	s, frame, closed := dial(t, cfg)
	if path := os.Getenv("RXT_RDP_SNAPSHOT"); path != "" { // compose every rectangle, save at the end
		s, frame, closed = snapshotDial(t, cfg, path)
	}
	defer s.Close()
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFrame(t, frame, closed, 30*time.Second)
	if d, _ := time.ParseDuration(os.Getenv("RXT_RDP_HOLD")); d > 0 { // stay connected, e.g. 15s
		n, area, end := 0, 0, time.After(d)
		for held := true; held; {
			select {
			case err := <-closed:
				t.Fatalf("closed while held: %v", err)
			case r := <-frame:
				n, area = n+1, area+r.W*r.H
			case <-end:
				held = false
			}
		}
		t.Logf("held %v: %d more updates, %d px (first rectangle of each)", d, n, area)
	}
}

// snapshotDial is dial that also draws every rectangle onto one image and writes it to path when
// the session closes (for a visual check of the decoders against a real server).
func snapshotDial(t *testing.T, cfg Config, path string) (*Session, chan Rect, chan error) {
	frame, closed := make(chan Rect, 1), make(chan error, 1)
	var mu sync.Mutex
	canvas := image.NewRGBA(image.Rect(0, 0, cfg.Width, cfg.Height))
	var s *Session
	s, _, _, err := Connect(cfg, func(rs []Rect, frames []uint32) {
		s.AckFrames(frames)
		if len(rs) == 0 {
			return
		}
		mu.Lock()
		for _, r := range rs {
			if img, err := png.Decode(bytes.NewReader(r.PNG)); err == nil {
				draw.Draw(canvas, image.Rect(r.X, r.Y, r.X+r.W, r.Y+r.H), img, image.Point{}, draw.Src)
			}
		}
		mu.Unlock()
		select {
		case frame <- rs[0]:
		default:
		}
	}, nil, func(e error) {
		select {
		case closed <- e:
		default:
		}
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		var buf bytes.Buffer
		_ = png.Encode(&buf, canvas)
		if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
			t.Errorf("snapshot: %v", err)
		}
	})
	return s, frame, closed
}

// envInt reads a positive integer from the environment, or def.
func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil && v > 0 {
		return v
	}
	return def
}

// TestEncodeCropsPadding: an update whose bitmap is padded to 4 px wide but covers 2 px on screen
// must produce a 2 px image (the padding columns drew black lines over the desktop).
func TestEncodeCropsPadding(t *testing.T) {
	rgba := bytes.Repeat([]byte{0xff, 0xff, 0xff, 0xff}, 4*2)
	r, ok := encode(rgba, 4, 2, 10, 5, 11, 6)
	if !ok || r.W != 2 || r.H != 2 || r.X != 10 || r.Y != 5 {
		t.Fatalf("encode = %+v ok=%v, want a 2x2 rect at 10,5", r, ok)
	}
	img, err := png.Decode(bytes.NewReader(r.PNG))
	if err != nil || img.Bounds().Dx() != 2 || img.Bounds().Dy() != 2 {
		t.Fatalf("png %v err %v", img.Bounds(), err)
	}
}

// bitmapUpdate builds a TS_UPDATE_BITMAP_DATA with one uncompressed 16 bpp rectangle.
func bitmapUpdate(x, y, w, h int, px []byte) []byte {
	b := []byte{1, 0, 1, 0} // UPDATETYPE_BITMAP, one rectangle
	for _, v := range []int{x, y, x + w - 1, y + h - 1, w, h, 16, 0, len(px)} {
		b = binary.LittleEndian.AppendUint16(b, uint16(v))
	}
	return append(b, px...)
}

// fpUpdate frames body as one TS_FP_UPDATE with the given fragmentation.
func fpUpdate(code, frag byte, body []byte) []byte {
	return append(binary.LittleEndian.AppendUint16([]byte{code | frag<<4}, uint16(len(body))), body...)
}

func TestFastPathReassemblesFragments(t *testing.T) {
	px := bytes.Repeat([]byte{0x1f, 0x00}, 4*2) // blue, RGB565
	upd := bitmapUpdate(3, 4, 4, 2, px)
	var f fastPath
	stream := append(fpUpdate(fpUpdateBitmap, fpFragFirst, upd[:10]), fpUpdate(0x5, fpFragSingle, nil)...) // + pointer-null
	if got := f.updates(stream); len(got) != 0 {
		t.Fatalf("first fragment produced %d updates", len(got))
	}
	got := f.updates(append(fpUpdate(fpUpdateBitmap, 0x3, upd[10:20]), fpUpdate(fpUpdateBitmap, fpFragLast, upd[20:])...))
	if len(got) != 1 || got[0].code != fpUpdateBitmap || !bytes.Equal(got[0].data, upd) {
		t.Fatalf("reassembled %d updates", len(got))
	}
	rs := bitmapRects(got[0].data, &pixelBudget{rect: 1 << 20, left: 1 << 20})
	if len(rs) != 1 || rs[0].X != 3 || rs[0].Y != 4 || rs[0].W != 4 || rs[0].H != 2 {
		t.Fatalf("rects %+v", rs)
	}
	img, err := png.Decode(bytes.NewReader(rs[0].PNG))
	if err != nil {
		t.Fatal(err)
	}
	if r, g, b, _ := img.At(0, 0).RGBA(); r != 0 || g != 0 || b>>8 != 0xff {
		t.Fatalf("pixel = %d,%d,%d, want blue", r, g, b)
	}
}

func TestSecurityOffer(t *testing.T) {
	if !useNLA(NLA) || !useNLA(Std) || useNLA(SSL) || useNLA("") {
		t.Fatal("NLA and Std offer CredSSP|TLS; SSL (and the default) offer TLS only")
	}
	cfg := tlsConfig(Config{Host: "h"})
	if !cfg.InsecureSkipVerify || cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("default TLS config %+v", cfg)
	}
	if tlsConfig(Config{LegacyTLS: true}).MinVersion != tls.VersionTLS10 {
		t.Fatal("LegacyTLS lowers the minimum to TLS 1.0")
	}
}

func TestWheelFlags(t *testing.T) {
	if f := wheelFlags(120); f != 0x0200|120 {
		t.Fatalf("up = %#x", f)
	}
	if f := wheelFlags(-120); f != 0x0200|0x0100|0x88 { // WHEEL | WHEEL_NEGATIVE | (-120 & 0xff)
		t.Fatalf("down = %#x", f)
	}
}
