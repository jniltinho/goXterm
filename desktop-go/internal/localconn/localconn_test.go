package localconn

import (
	"bytes"
	"testing"
	"time"
)

// TestLocalShellEcho starts a local /bin/sh in a PTY, runs an echo, and checks the output comes back
// through the read loop — exercising Start, the streaming and Write against a real PTY.
func TestLocalShellEcho(t *testing.T) {
	got := make(chan []byte, 64)
	s, err := Start("/bin/sh", 80, 24, func(b []byte) { got <- append([]byte(nil), b...) }, func(error) {})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()

	if err := s.Write([]byte("echo rxt-local-ok\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	var buf []byte
	for {
		select {
		case b := <-got:
			buf = append(buf, b...)
			if bytes.Contains(buf, []byte("rxt-local-ok")) {
				return
			}
		case <-deadline:
			t.Fatalf("no echo within 5s; got %q", buf)
		}
	}
}
