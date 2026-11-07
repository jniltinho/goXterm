package sftpconn

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"

	"rustxterm-go/internal/sshconn"
)

// TestLabSFTP round-trips a file through the protocol lab's sshd (alice / lab-alice-pass on
// 127.0.0.1:2222). Needs the lab up; skipped unless RXT_SSH_LAB=1. Lab credentials are fictional.
func TestLabSFTP(t *testing.T) {
	if os.Getenv("RXT_SSH_LAB") == "" {
		t.Skip("set RXT_SSH_LAB=1 with the lab sshd up (127.0.0.1:2222)")
	}
	c, err := Connect(sshconn.Config{
		Host: "127.0.0.1", Port: 2222, User: "alice", Password: "lab-alice-pass",
		HostKey: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()

	home, err := c.Home()
	if err != nil || home == "" {
		t.Fatalf("home: %q err=%v", home, err)
	}
	if _, err := c.List(home); err != nil {
		t.Fatalf("list home: %v", err)
	}

	// upload a local temp file, then download it back and compare
	dir := t.TempDir()
	local := filepath.Join(dir, "rxt-upload.txt")
	want := []byte("rustXterm SFTP round-trip test\n")
	if err := os.WriteFile(local, want, 0o600); err != nil {
		t.Fatal(err)
	}
	remote := home + "/rxt-sftp-test.txt"
	if n, err := c.Upload(local, remote); err != nil || n != int64(len(want)) {
		t.Fatalf("upload: n=%d err=%v", n, err)
	}
	t.Cleanup(func() { _ = c.Remove(remote) })

	back := filepath.Join(dir, "rxt-download.txt")
	if n, err := c.Download(remote, back); err != nil || n != int64(len(want)) {
		t.Fatalf("download: n=%d err=%v", n, err)
	}
	got, err := os.ReadFile(back)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("round-trip mismatch: err=%v", err)
	}
}
