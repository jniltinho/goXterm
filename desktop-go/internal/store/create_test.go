package store

import (
	"testing"
)

// A created store opens with its password, has the full schema, takes writes, and refuses a second
// Create or a wrong password.
func TestCreateOpenWrite(t *testing.T) {
	dir := t.TempDir()
	if err := Create(dir, "lab-master", nil); err != nil {
		t.Fatal(err)
	}
	if err := Create(dir, "other", nil); err == nil {
		t.Fatal("Create replaced an existing store")
	}
	if _, err := Open(dir, "wrong"); err == nil {
		t.Fatal("wrong password opened the store")
	}
	s, err := Open(dir, "lab-master")
	if err != nil {
		t.Fatal(err)
	}
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != len(migrations) {
		t.Fatalf("user_version %d, %v", v, err)
	}
	id, err := s.SaveSession(`{"id":0,"name":"lab","protocol":"ssh","host":"sshd","port":22,"user":"alice"}`)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir, "lab-master")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.Sessions()
	if err != nil || len(rows) != 1 || asID(rows[0]["id"]) != id {
		t.Fatalf("sessions after reopen: %v %v", rows, err)
	}
}

func asID(v any) int64 { i, _ := v.(int64); return i }

type mapKeyring map[string][]byte

func (m mapKeyring) get(id string) ([]byte, bool, error) { b, ok := m[id]; return b, ok, nil }
func (m mapKeyring) set(id string, b []byte) error       { m[id] = b; return nil }

// With a keyring the store opens silently through the keyring wrap and still with the password.
func TestCreateWithKeyring(t *testing.T) {
	dir, kr := t.TempDir(), mapKeyring{}
	if err := Create(dir, "lab-master", kr); err != nil {
		t.Fatal(err)
	}
	if len(kr) != 1 {
		t.Fatalf("keyring entries: %d", len(kr))
	}
	s, ok, err := OpenWithKeyring(dir, kr)
	if err != nil || !ok {
		t.Fatalf("keyring open: %v %v", ok, err)
	}
	s.Close()
	if s, err = Open(dir, "lab-master"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if Create(t.TempDir(), "lab-master", NoKeyring{}) == nil {
		t.Fatal("a keyring error must abort store creation")
	}
}
