package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteRealCopy exercises the full write-back path against a COPY of a real store (never the
// original): save a session, reopen and confirm it decrypts and is present, delete it, reopen and
// confirm it is gone and the count is back. Gated: set RXT_WRITE_TEST=1, RXT_STORE_DIR to the copy
// and RXT_MASTER_PASSWORD. The store dir must hold rustxterm.db and the keys.v1 envelope.
func TestWriteRealCopy(t *testing.T) {
	if os.Getenv("RXT_WRITE_TEST") == "" {
		t.Skip("set RXT_WRITE_TEST=1 with RXT_STORE_DIR (a COPY) and RXT_MASTER_PASSWORD")
	}
	dir := os.Getenv("RXT_STORE_DIR")
	pw := os.Getenv("RXT_MASTER_PASSWORD")

	st, err := Open(dir, pw)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	before, err := st.Sessions()
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	n0 := len(before)
	const js = `{"id":0,"name":"__wtest__","protocol":"serial","host":"/dev/ttyTEST","port":0,` +
		`"serial":{"port":"/dev/ttyTEST","line":{"baud":9600,"data_bits":"8","parity":"none","stop_bits":"1"}}}`
	id, err := st.SaveSession(js)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	st.Close()
	t.Logf("saved session id=%d", id)

	// Reopen: the re-encrypted database must decrypt and contain the new session.
	st2, err := Open(dir, pw)
	if err != nil {
		t.Fatalf("reopen after save (re-encryption broke the db?): %v", err)
	}
	rows, _ := st2.Sessions()
	if len(rows) != n0+1 {
		t.Fatalf("session count %d, want %d", len(rows), n0+1)
	}
	found := false
	for _, m := range rows {
		if m["id"].(int64) == id && m["name"] == "__wtest__" && m["host"] == "/dev/ttyTEST" {
			found = true
			if ser, ok := m["serial"].(map[string]any); ok {
				if line, ok := ser["line"].(map[string]any); ok && line["baud"].(float64) == 9600 {
					t.Log("nested serial.line.baud round-tripped")
				}
			}
		}
	}
	if !found {
		t.Fatal("saved session not found after reopen")
	}

	if err := st2.DeleteSession(id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	st2.Close()

	st3, err := Open(dir, pw)
	if err != nil {
		t.Fatalf("reopen after delete: %v", err)
	}
	rows3, _ := st3.Sessions()
	st3.Close()
	if len(rows3) != n0 {
		t.Fatalf("after delete count %d, want %d", len(rows3), n0)
	}
	if _, err := os.Stat(filepath.Join(dir, "rustxterm.db.bak")); err != nil {
		t.Fatalf("no backup written: %v", err)
	}
}
