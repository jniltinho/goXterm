package rdpconn

import (
	"os"
	"strconv"
	"testing"
	"time"

	"rustxterm-go/internal/grdp/client"
)

// TestLabRDP connects to the protocol lab's xrdp (alice / lab-alice-pass on 127.0.0.1:13389, TLS/SSL
// security) and checks at least one bitmap rectangle decodes to a PNG. Needs the lab up; skipped
// unless RXT_SSH_LAB=1. Lab credentials are fictional.
func TestLabRDP(t *testing.T) {
	if os.Getenv("RXT_SSH_LAB") == "" {
		t.Skip("set RXT_SSH_LAB=1 with the lab rdp up (127.0.0.1:13389)")
	}
	frame := make(chan Rect, 1)
	closed := make(chan error, 1)
	s, w, h, err := Connect(Config{
		Host: "127.0.0.1", Port: 13389, User: "alice", Password: "lab-alice-pass",
		Width: 1024, Height: 768, Protocol: SSL,
	}, func(r Rect) {
		select {
		case frame <- r:
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
	defer s.Close()
	t.Logf("requested desktop %dx%d", w, h)
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case r := <-frame:
		if len(r.PNG) == 0 {
			t.Fatal("empty frame PNG")
		}
		t.Logf("frame %dx%d at %d,%d, %d-byte png", r.W, r.H, r.X, r.Y, len(r.PNG))
	case e := <-closed:
		t.Fatalf("closed before a frame: %v", e)
	case <-time.After(20 * time.Second):
		t.Fatal("no bitmap update in time")
	}
}

// TestRealRDP connects to an arbitrary RDP server from the environment and captures one frame
// (connectivity/decode check only — no input is sent, nothing on the server is changed). All
// connection details, including the password, come from env so no credentials live in the source.
// Skipped unless RXT_RDP_REAL=1. Env: RXT_RDP_HOST, RXT_RDP_PORT (default 3389), RXT_RDP_USER,
// RXT_RDP_PASS, RXT_RDP_PROTO (ssl|nla|std, default nla).
func TestRealRDP(t *testing.T) {
	if os.Getenv("RXT_RDP_REAL") == "" {
		t.Skip("set RXT_RDP_REAL=1 with RXT_RDP_HOST/USER/PASS to run")
	}
	port, _ := strconv.Atoi(os.Getenv("RXT_RDP_PORT"))
	if port == 0 {
		port = 3389
	}
	proto := Protocol(os.Getenv("RXT_RDP_PROTO"))
	if proto == "" {
		proto = NLA
	}
	frame := make(chan Rect, 1)
	closed := make(chan error, 1)
	s, w, h, err := Connect(Config{
		Host: os.Getenv("RXT_RDP_HOST"), Port: port,
		User: os.Getenv("RXT_RDP_USER"), Password: os.Getenv("RXT_RDP_PASS"),
		Width: 1024, Height: 768, Protocol: proto,
		LegacyTLS: os.Getenv("RXT_RDP_LEGACY") != "",
	}, func(r Rect) {
		select {
		case frame <- r:
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
	defer s.Close()
	t.Logf("requested desktop %dx%d (proto %s)", w, h, proto)
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case r := <-frame:
		if len(r.PNG) == 0 {
			t.Fatal("empty frame PNG")
		}
		t.Logf("frame %dx%d at %d,%d, %d-byte png", r.W, r.H, r.X, r.Y, len(r.PNG))
	case e := <-closed:
		t.Fatalf("closed before a frame: %v", e)
	case <-time.After(30 * time.Second):
		t.Fatal("no bitmap update in time")
	}
}

// TestEncodeCropsPadding: an update whose bitmap is padded to 4 px wide but covers 2 px on screen
// must produce a 2 px image (the padding columns drew black lines over the desktop).
func TestEncodeCropsPadding(t *testing.T) {
	px := []byte{0xff, 0xff} // white, RGB565 LE
	b := client.Bitmap{DestLeft: 10, DestTop: 5, DestRight: 11, DestBottom: 6, Width: 4, Height: 2, BitsPerPixel: 2}
	for i := 0; i < 8; i++ {
		b.Data = append(b.Data, px...)
	}
	r, ok := encode(b)
	if !ok || r.W != 2 || r.H != 2 || r.X != 10 || r.Y != 5 {
		t.Fatalf("encode = %+v ok=%v, want a 2x2 rect at 10,5", r, ok)
	}
}
