package store

import (
	"testing"
)

// A created store opens with its password, has the full schema, takes writes, and refuses a second
// Create or a wrong password.
func TestCreateOpenWrite(t *testing.T) {
	dir := t.TempDir()
	if err := Create(dir, "lab-master"); err != nil {
		t.Fatal(err)
	}
	if err := Create(dir, "other"); err == nil {
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
