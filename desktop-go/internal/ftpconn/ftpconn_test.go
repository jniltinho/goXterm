package ftpconn

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// roundtrip connects, lists the home directory and round-trips a file. The lab is disposable, so it
// does not clean up. Lab credentials are fictional.
func roundtrip(t *testing.T, cfg Config) {
	t.Helper()
	c, err := Connect(cfg)
	if err != nil {
		t.Fatalf("connect (%s): %v", cfg.Mode, err)
	}
	defer c.Close()

	home, err := c.Home()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	if _, err := c.List(home); err != nil {
		t.Fatalf("list %q: %v", home, err)
	}

	dir := t.TempDir()
	local := filepath.Join(dir, "rxt-up.txt")
	want := []byte("rustXterm FTP(S) round-trip — " + string(cfg.Mode) + "\n")
	if err := os.WriteFile(local, want, 0o600); err != nil {
		t.Fatal(err)
	}
	remote := "rxt-ftp-test-" + string(cfg.Mode) + ".txt"
	if n, err := c.Upload(local, remote); err != nil || n != int64(len(want)) {
		t.Fatalf("upload: n=%d err=%v", n, err)
	}
	back := filepath.Join(dir, "rxt-down.txt")
	if n, err := c.Download(remote, back); err != nil || n != int64(len(want)) {
		t.Fatalf("download: n=%d err=%v", n, err)
	}
	got, err := os.ReadFile(back)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("round-trip mismatch: err=%v", err)
	}
}

// TestLabFTPSExplicit uses explicit FTPS (AUTH TLS) on the lab's ftp service (127.0.0.1:2121).
func TestLabFTPSExplicit(t *testing.T) {
	if os.Getenv("RXT_SSH_LAB") == "" {
		t.Skip("set RXT_SSH_LAB=1 with the lab ftp up (127.0.0.1:2121)")
	}
	roundtrip(t, Config{Host: "127.0.0.1", Port: 2121, User: "alice", Password: "lab-alice-pass", Mode: Explicit, TLSInsecure: true})
}

// TestLabFTPSImplicit uses implicit FTPS on the lab's ftps-implicit service (127.0.0.1:2990).
func TestLabFTPSImplicit(t *testing.T) {
	if os.Getenv("RXT_SSH_LAB") == "" {
		t.Skip("set RXT_SSH_LAB=1 with the lab ftps-implicit up (127.0.0.1:2990)")
	}
	roundtrip(t, Config{Host: "127.0.0.1", Port: 2990, User: "alice", Password: "lab-alice-pass", Mode: Implicit, TLSInsecure: true})
}
