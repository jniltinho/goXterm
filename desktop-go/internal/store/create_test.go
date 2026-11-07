package store

import (
	"strconv"
	"strings"
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

// Folder icon, moving a session and deleting a folder (contents go to the parent, as in Rust).
func TestFolderEdits(t *testing.T) {
	dir := t.TempDir()
	if err := Create(dir, "lab-master", nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, "lab-master")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	top, _ := s.SaveFolder(0, "Lab", nil)
	sub, _ := s.SaveFolder(0, "Servers", &top)
	id, err := s.SaveSession(`{"id":0,"name":"lab","protocol":"ssh","host":"sshd","port":22,"folder":` + itoa(sub) + `}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetFolderIcon(top, "folder-green"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFolder(sub); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.Sessions()
	if f, _ := rows[0]["folder"].(int64); f != top {
		t.Fatalf("session after deleting its folder: folder %v, want parent %d", rows[0]["folder"], top)
	}
	if err := s.MoveSession(id, nil); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.Sessions()
	var js string
	_ = s.db.QueryRow(`SELECT json FROM sessions WHERE id=?`, id).Scan(&js)
	if rows[0]["folder"] != nil || !strings.Contains(js, `"folder":null`) {
		t.Fatalf("move to root: %v %s", rows[0]["folder"], js)
	}
	fs, _ := s.Folders()
	if len(fs) != 1 || fs[0]["icon"] != "folder-green" {
		t.Fatalf("folders: %v", fs)
	}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }
