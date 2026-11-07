package vncconn

import (
	"os"
	"testing"
	"time"
)

// TestLabVNC connects to the protocol lab's TigerVNC server (password lab-vnc1 on 127.0.0.1:5901),
// checks the desktop size and that at least one framebuffer rectangle decodes to a PNG. Needs the lab
// up; skipped unless RXT_SSH_LAB=1. Lab credentials are fictional.
func TestLabVNC(t *testing.T) {
	if os.Getenv("RXT_SSH_LAB") == "" {
		t.Skip("set RXT_SSH_LAB=1 with the lab vnc up (127.0.0.1:5901)")
	}
	initCh := make(chan [2]int, 1)
	frameCh := make(chan Rect, 1)
	s, err := Connect(Config{Host: "127.0.0.1", Port: 5901, Password: "lab-vnc1"},
		func(w, h int, _ string) {
			select {
			case initCh <- [2]int{w, h}:
			default:
			}
		},
		func(r Rect) {
			select {
			case frameCh <- r:
			default:
			}
		},
		nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer s.Close()

	select {
	case wh := <-initCh:
		if wh[0] <= 0 || wh[1] <= 0 {
			t.Fatalf("bad desktop size %v", wh)
		}
		t.Logf("desktop %dx%d", wh[0], wh[1])
	case <-time.After(5 * time.Second):
		t.Fatal("no ServerInit")
	}
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case r := <-frameCh:
		if len(r.PNG) == 0 {
			t.Fatal("empty frame PNG")
		}
		t.Logf("frame %dx%d at %d,%d, %d-byte png", r.W, r.H, r.X, r.Y, len(r.PNG))
	case <-time.After(8 * time.Second):
		t.Fatal("no framebuffer update")
	}
}
