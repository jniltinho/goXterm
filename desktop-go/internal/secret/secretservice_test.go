//go:build linux

package secret

import (
	"bytes"
	"os"
	"testing"
)

// TestKeyringLive round-trips a throwaway item through the session Secret Service. It needs a desktop
// session (D-Bus + a keyring daemon), so it is skipped unless RXT_KEYRING_LIVE=1. It cleans up after
// itself and uses an obvious test id so it cannot collide with real rustXterm keys.
func TestKeyringLive(t *testing.T) {
	if os.Getenv("RXT_KEYRING_LIVE") == "" {
		t.Skip("set RXT_KEYRING_LIVE=1 to run against the session Secret Service")
	}
	s := New()
	const id = "rustxterm-keyring-selftest-DO-NOT-USE"
	want := []byte("rustxterm secret service self-test")

	if err := s.Set(id, want); err != nil {
		t.Fatalf("set: %v", err)
	}
	t.Cleanup(func() { _ = s.Delete(id) })

	got, ok, err := s.Get(id)
	if err != nil || !ok {
		t.Fatalf("get after set: ok=%v err=%v", ok, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round-trip mismatch: got %d bytes, want %d", len(got), len(want))
	}
	if err := s.Delete(id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := s.Get(id); ok {
		t.Fatal("item still present after delete")
	}
}
