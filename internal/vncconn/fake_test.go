package vncconn

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeServer drives the server side of an in-memory RFB connection: VncAuth accepted, then
// ServerInit with the given size. Bytes the client sends after the handshake arrive on got.
type fakeServer struct {
	conn net.Conn
	got  chan []byte
}

func startFake(t *testing.T, w, h uint16) (Config, *fakeServer) {
	t.Helper()
	cli, srv := net.Pipe()
	fs := &fakeServer{conn: srv, got: make(chan []byte, 64)}
	go func() {
		read := func(n int) { b := make([]byte, n); _, _ = io.ReadFull(srv, b) }
		_, _ = srv.Write([]byte("RFB 003.008\n"))
		read(12)
		_, _ = srv.Write([]byte{1, 2}) // one type: VncAuth
		read(1)
		_, _ = srv.Write(make([]byte, 16)) // challenge
		read(16)
		_, _ = srv.Write([]byte{0, 0, 0, 0}) // auth OK
		read(1)                              // ClientInit
		var si bytes.Buffer
		_ = binary.Write(&si, binary.BigEndian, struct {
			W, H    uint16
			PF      [16]byte
			NameLen uint32
		}{w, h, [16]byte{}, 4})
		si.WriteString("fake")
		_, _ = srv.Write(si.Bytes())
		for { // everything else the client sends
			b := make([]byte, 64)
			n, err := srv.Read(b)
			if err != nil {
				close(fs.got)
				return
			}
			fs.got <- b[:n]
		}
	}()
	return Config{Host: "fake", Port: 5900, Password: "x", Dial: func(string, string) (net.Conn, error) { return cli, nil }}, fs
}

// update writes a FramebufferUpdate with one Raw rectangle (pixels only when they fit a sane size).
func (f *fakeServer) update(x, y, w, h uint16) {
	var b bytes.Buffer
	b.Write([]byte{0, 0})
	_ = binary.Write(&b, binary.BigEndian, uint16(1))
	_ = binary.Write(&b, binary.BigEndian, struct {
		X, Y, W, H uint16
		Enc        int32
	}{x, y, w, h, encRaw})
	if int(w)*int(h) <= 64*64 {
		b.Write(make([]byte, int(w)*int(h)*4))
	}
	go func() { _, _ = f.conn.Write(b.Bytes()) }()
}

// requests counts FramebufferUpdateRequest messages (type 3, 10 bytes) seen within d.
func (f *fakeServer) requests(d time.Duration) int {
	n := 0
	deadline := time.After(d)
	for {
		select {
		case b, ok := <-f.got:
			if !ok {
				return n
			}
			for i := 0; i+10 <= len(b); i++ {
				if b[i] == 3 {
					n++
					i += 9
				}
			}
		case <-deadline:
			return n
		}
	}
}

func TestHostileDesktopSizeRefused(t *testing.T) {
	cfg, _ := startFake(t, 60000, 60000)
	if _, err := Connect(cfg, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("a 60000×60000 desktop was accepted: %v", err)
	}
}

func TestRectangleOutsideTheDesktopClosesTheSession(t *testing.T) {
	cfg, fs := startFake(t, 64, 48)
	closed := make(chan error, 1)
	s, err := Connect(cfg, nil, nil, func(err error) { closed <- err })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Start()
	fs.update(0, 0, 60000, 60000) // would need 13 GiB of pixels
	select {
	case err := <-closed:
		if err == nil || !strings.Contains(err.Error(), "outside the desktop") {
			t.Fatalf("closed with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hostile rectangle did not close the session")
	}
}

func TestPausedSessionRequestsNothingUntilShown(t *testing.T) {
	cfg, fs := startFake(t, 64, 48)
	s, err := Connect(cfg, nil, func(Rect) {}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	go func() { _ = s.Start() }()
	if n := fs.requests(time.Second); n < 1 {
		t.Fatal("no first update request")
	}
	_ = s.SetPaused(true)
	fs.update(0, 0, 2, 2)
	if n := fs.requests(400 * time.Millisecond); n != 0 {
		t.Fatalf("%d update requests while hidden", n)
	}
	go func() { _ = s.SetPaused(false) }()
	if n := fs.requests(time.Second); n != 1 {
		t.Fatalf("%d update requests after showing, want the owed one", n)
	}
}
