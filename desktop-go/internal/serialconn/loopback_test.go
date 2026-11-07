package serialconn

import (
	"os"
	"testing"
	"time"
)

// TestSerialLoopback opens RXT_SERIAL_DEV with serialconn and exchanges bytes with its peer
// RXT_SERIAL_PEER (the other end of a virtual null-modem, e.g. a socat pty pair), proving the
// read loop and Write work against a real device node. Gated; set both env vars.
func TestSerialLoopback(t *testing.T) {
	dev, peer := os.Getenv("RXT_SERIAL_DEV"), os.Getenv("RXT_SERIAL_PEER")
	if dev == "" || peer == "" {
		t.Skip("set RXT_SERIAL_DEV and RXT_SERIAL_PEER to a pty/null-modem pair")
	}
	got := make(chan []byte, 8)
	s, err := Connect(Config{Device: dev, Baud: 9600}, func(b []byte) {
		got <- append([]byte(nil), b...)
	}, func(error) {})
	if err != nil {
		t.Fatalf("open %s: %v", dev, err)
	}
	defer s.Close()

	// peer end as a plain file: write to it, serialconn should read it.
	pf, err := os.OpenFile(peer, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open peer %s: %v", peer, err)
	}
	defer pf.Close()

	if _, err := pf.WriteString("ping"); err != nil {
		t.Fatal(err)
	}
	select {
	case b := <-got:
		if string(b) != "ping" {
			t.Fatalf("read %q, want %q", b, "ping")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serialconn did not read from the device")
	}

	// the other direction: serialconn.Write should reach the peer.
	if err := s.Write([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	pf.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := pf.Read(buf)
	if err != nil {
		t.Fatalf("peer read: %v", err)
	}
	if string(buf[:n]) != "pong" {
		t.Fatalf("peer got %q, want %q", buf[:n], "pong")
	}
}
