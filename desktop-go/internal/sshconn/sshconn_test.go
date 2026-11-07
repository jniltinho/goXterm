package sshconn

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestLabEcho connects to the protocol lab's sshd (alice / lab-alice-pass on 127.0.0.1:2222), sends a
// command and checks its output comes back — the full bidirectional path. Needs the lab up and
// reachable; skipped unless RXT_SSH_LAB=1. Lab credentials are fictional.
func TestLabEcho(t *testing.T) {
	if os.Getenv("RXT_SSH_LAB") == "" {
		t.Skip("set RXT_SSH_LAB=1 with the lab sshd up (127.0.0.1:2222)")
	}
	var mu sync.Mutex
	var buf strings.Builder
	done := make(chan struct{}, 1)
	onData := func(b []byte) {
		mu.Lock()
		buf.Write(b)
		if strings.Contains(buf.String(), "RXT_LAB_ECHO_OK") {
			select {
			case done <- struct{}{}:
			default:
			}
		}
		mu.Unlock()
	}
	s, err := Connect(Config{
		Host: "127.0.0.1", Port: 2222, User: "alice", Password: "lab-alice-pass",
		HostKey: ssh.InsecureIgnoreHostKey(), Cols: 80, Rows: 24,
	}, onData, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer s.Close()

	if err := s.Write([]byte("echo RXT_LAB_ECHO_OK\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case <-done:
		// got the echoed marker back from the remote shell
	case <-time.After(10 * time.Second):
		mu.Lock()
		got := buf.String()
		mu.Unlock()
		t.Fatalf("did not see command output in time; got %d bytes", len(got))
	}
}
