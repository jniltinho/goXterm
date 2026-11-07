//go:build linux

package control

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNotRunning(t *testing.T) {
	if _, err := Connect(t.TempDir()); !IsNotRunning(err) {
		t.Fatalf("connect with no socket must be not-running, got %v", err)
	}
}

func TestListenPermsAndInUse(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rustxterm")
	l, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if m := mode(t, dir); m != 0o700 {
		t.Errorf("dir mode = %o, want 700", m)
	}
	if m := mode(t, filepath.Join(dir, socketName)); m != 0o600 {
		t.Errorf("socket mode = %o, want 600", m)
	}
	if _, err := Listen(dir); err == nil {
		t.Error("a second listen must not steal a live socket")
	}
	sock := filepath.Join(dir, socketName)
	l.Close()
	if _, err := os.Lstat(sock); err == nil {
		t.Error("socket must be removed on Close")
	}
}

func TestRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rustxterm")
	l, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // server: echo the request back as the result
		defer wg.Done()
		c, err := l.Accept()
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer c.Close()
		var req Request
		if ok, err := ReadMessage(c, &req); !ok || err != nil {
			t.Errorf("server read: ok=%v err=%v", ok, err)
			return
		}
		_ = WriteMessage(c, OK(req.ID, map[string]any{"op": req.Op, "tab": req.Tab}))
	}()

	c, err := Connect(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tab := uint64(3)
	req := Request{ID: 9, Client: "t", Entry: EntryCliLive, AgentOp: AgentOp{Op: "send", Tab: &tab, Data: "ls\n"}}
	if err := WriteMessage(c, req); err != nil {
		t.Fatal(err)
	}
	var resp Response
	if ok, err := ReadMessage(c, &resp); !ok || err != nil {
		t.Fatalf("client read: ok=%v err=%v", ok, err)
	}
	if resp.Status != "ok" || resp.ID != 9 {
		t.Fatalf("response = %+v", resp)
	}
	wg.Wait()
}

func TestStaleSocketReplaced(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rustxterm")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A crashed instance leaves the file behind with nobody listening.
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, socketName), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.Close() // closes the listener but the bound path may linger
	_, _ = os.Create(filepath.Join(dir, socketName))
	l, err := Listen(dir)
	if err != nil {
		t.Fatalf("a stale socket must be replaced, got %v", err)
	}
	l.Close()
}

func TestPrivateDirRefused(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "rustxterm")
	if err := os.Mkdir(dir, 0o755); err != nil { // group/other accessible
		t.Fatal(err)
	}
	uid := uint32(os.Getuid())
	if err := privateDir(dir, uid); err == nil {
		t.Error("a 0755 runtime dir must be refused")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := privateDir(dir, uid); err != nil {
		t.Errorf("a 0700 dir owned by us must pass, got %v", err)
	}
	if err := privateDir(dir, uid+1); err == nil {
		t.Error("a dir owned by another uid must be refused")
	}
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}
