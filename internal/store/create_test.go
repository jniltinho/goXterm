package store

import (
	"strconv"
	"strings"
	"testing"
	"time"
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
func (m mapKeyring) delete(id string) error              { delete(m, id); return nil }

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

// Import creates folder paths once, reuses existing folders and adds the sessions.
func TestImportSessions(t *testing.T) {
	dir := t.TempDir()
	if err := Create(dir, "lab-master", nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, "lab-master")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lab, _ := s.SaveFolder(0, "Lab", nil)
	n, err := s.ImportSessions([]ImportItem{
		{Folder: "Lab/Staging", Session: map[string]any{"name": "a", "protocol": "ssh", "host": "a.example", "port": 22}},
		{Folder: "Lab/Staging", Session: map[string]any{"name": "b", "protocol": "ssh", "host": "b.example", "port": 22}},
		{Folder: "", Session: map[string]any{"name": "c", "protocol": "telnet", "host": "c.example", "port": 23}},
	})
	if err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
	fs, _ := s.Folders()
	if len(fs) != 2 {
		t.Fatalf("folders %v", fs)
	}
	var staging int64
	for _, f := range fs {
		if f["name"] == "Staging" {
			staging = f["id"].(int64)
			if f["parent"] != lab {
				t.Fatalf("Staging parent %v, want existing Lab %d", f["parent"], lab)
			}
		}
	}
	rows, _ := s.Sessions()
	in := 0
	for _, r := range rows {
		if r["folder"] == staging {
			in++
		}
	}
	if len(rows) != 3 || in != 2 {
		t.Fatalf("sessions %v", rows)
	}
}

// Change the master password, then turn the keyring wrap on and off (Rust envelope edits).
func TestChangePasswordAndKeyringToggle(t *testing.T) {
	dir, kr := t.TempDir(), mapKeyring{}
	if err := Create(dir, "old-master", nil); err != nil {
		t.Fatal(err)
	}
	if ChangePassword(dir, "wrong", nil, "new-master") == nil {
		t.Fatal("changed with a wrong password")
	}
	if err := ChangePassword(dir, "old-master", nil, "new-master"); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, "old-master"); err == nil {
		t.Fatal("old password still opens")
	}
	if HasKeyring(dir) {
		t.Fatal("keyring before enabling")
	}
	if err := SetKeyring(dir, "new-master", kr, true); err != nil || !HasKeyring(dir) || len(kr) != 1 {
		t.Fatalf("enable: %v %v %d", err, HasKeyring(dir), len(kr))
	}
	if s, ok, err := OpenWithKeyring(dir, kr); err != nil || !ok {
		t.Fatalf("keyring open: %v %v", ok, err)
	} else {
		s.Close()
	}
	if err := SetKeyring(dir, "new-master", kr, false); err != nil || HasKeyring(dir) || len(kr) != 0 {
		t.Fatalf("disable: %v %v %d", err, HasKeyring(dir), len(kr))
	}
	s, err := Open(dir, "new-master")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	if err := Create(dir, "lab-master", nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, "lab-master")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// Export → import into an empty store keeps folders (with icons), sessions (fields, folder) and
// tunnels (re-pointed); the Rust tests' format, version, credential and local-entry rules hold.
func TestTOMLRoundTripAndRules(t *testing.T) {
	src := newStore(t)
	prod, _ := src.SaveFolder(0, "Production", nil)
	db, _ := src.SaveFolder(0, "Databases", &prod)
	_ = src.SetFolderIcon(db, "folder-green")
	app, err := src.SaveSession(`{"id":0,"name":"app","protocol":"ssh","host":"app.example","port":2022,"user":"alice","folder":` + itoa(db) +
		`,"ssh":{"key_file":"~/.ssh/id_ed25519","keepalive_secs":30,"jump_hosts":[{"host":"bastion.example","port":2200,"user":"bob"}]},"terminal":{"font_size":12.5,"scheme":null}}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := src.SaveTunnel(`{"name":"db","kind":"remote","via":` + itoa(app) + `,"bind_addr":"0.0.0.0","bind_port":15432,"dest_host":"db.internal","dest_port":5432}`); err != nil {
		t.Fatal(err)
	}
	text, err := src.ExportTOML()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, "format = \"rustxterm-export\"\nversion = 1\n") || strings.Contains(text, "null") ||
		!strings.Contains(text, "port = 2022\n") || !strings.Contains(text, "font_size = 12.5") {
		t.Fatalf("export:\n%s", text)
	}

	dst := newStore(t)
	n, local, err := dst.ImportTOML(text)
	if err != nil || n != 1 || local != 0 {
		t.Fatalf("import: %d %d %v", n, local, err)
	}
	ss, _ := dst.Sessions()
	fs, _ := dst.Folders()
	ts, _ := dst.Tunnels()
	if len(ss) != 1 || len(fs) != 2 || len(ts) != 1 {
		t.Fatalf("after import: %d sessions %d folders %d tunnels", len(ss), len(fs), len(ts))
	}
	got := ss[0]
	var dbID int64
	for _, f := range fs {
		if f["name"] == "Databases" {
			dbID = f["id"].(int64)
			if f["icon"] != "folder-green" {
				t.Fatalf("icon %v", f["icon"])
			}
		}
	}
	jumps, _ := got["ssh"].(map[string]any)["jump_hosts"].([]any)
	if got["folder"] != dbID || got["port"] != float64(2022) || len(jumps) != 1 || ts[0]["via"] != got["id"] {
		t.Fatalf("session %v tunnel %v", got, ts[0])
	}

	for _, bad := range []string{"format = \"other\"\nversion = 1\n", "format = \"rustxterm-export\"\nversion = 2\n", "not toml ["} {
		if _, _, err := dst.ImportTOML(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	n, local, err = dst.ImportTOML("format = \"rustxterm-export\"\nversion = 1\n[[sessions]]\nname = \"me\"\nprotocol = \"local\"\n" +
		"[[sessions]]\nname = \"a\"\nhost = \"a.example\"\npassword = \"FICTIONAL\"\n")
	if err != nil || n != 1 || local != 1 {
		t.Fatalf("local/credential: %d %d %v", n, local, err)
	}
	var js string
	_ = dst.db.QueryRow(`SELECT json FROM sessions WHERE name='a'`).Scan(&js)
	if strings.Contains(js, "FICTIONAL") {
		t.Fatalf("credential kept: %s", js)
	}
}

func TestPolicyAndKnownUsers(t *testing.T) {
	dir := t.TempDir()
	if err := Create(dir, "lab-master", nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, "lab-master")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, err := s.SaveSession(`{"id":0,"name":"a","protocol":"ssh","host":"sshd","user":"alice"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSession(`{"id":0,"name":"b","protocol":"ssh","host":"sshd","user":"Bob"}`); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Policy(id); err != nil || p != (Policy{Policy: "deny"}) {
		t.Fatalf("default policy %+v %v", p, err)
	}
	if err := s.SetPolicy(id, Policy{Policy: "ask", ContentRead: true}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Policy(id); p != (Policy{Policy: "ask", ContentRead: true}) {
		t.Fatalf("policy %+v", p)
	}
	if err := s.SetPolicy(id, Policy{Policy: "root"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Policy(id); p.Policy != "deny" {
		t.Fatalf("unknown policy not denied: %+v", p)
	}
	if u, err := s.KnownUsers(); err != nil || len(u) != 2 || u[0] != "alice" || u[1] != "Bob" {
		t.Fatalf("users %v %v", u, err)
	}
}

func TestSaveSessionBadFolderIsRoot(t *testing.T) {
	dir := t.TempDir()
	if err := Create(dir, "lab-master", nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, "lab-master")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// The UI once sent a click event as the folder ({"isTrusted":true}); it must land at the root.
	for _, js := range []string{`{"id":0,"name":"a","host":"h","folder":{"isTrusted":true}}`, `{"id":0,"name":"b","host":"h","folder":0}`} {
		id, err := s.SaveSession(js)
		if err != nil {
			t.Fatalf("%s: %v", js, err)
		}
		var doc string
		if err := s.db.QueryRow(`SELECT json FROM sessions WHERE id=? AND folder IS NULL`, id).Scan(&doc); err != nil || !strings.Contains(doc, `"folder":null`) {
			t.Fatalf("%s stored as %q (%v)", js, doc, err)
		}
	}
}

func TestSetCredentialReplaces(t *testing.T) {
	dir := t.TempDir()
	if err := Create(dir, "lab-master", nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, "lab-master")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.SaveSession(`{"id":0,"name":"a","protocol":"ssh","host":"sshd","user":"alice"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, pw := range []string{"first-pass", "lab-alice-pass"} {
		if err := s.SetCredential(id, CredPassword, []byte(pw)); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	if s, err = Open(dir, "lab-master"); err != nil { // survives the write-back
		t.Fatal(err)
	}
	defer s.Close()
	if b, ok, err := s.Credential(id, CredPassword); err != nil || !ok || string(b) != "lab-alice-pass" {
		t.Fatalf("credential ok=%v err=%v", ok, err)
	}
	if n, _ := s.SavedCredentialCount(); n != 1 {
		t.Fatalf("%d rows", n)
	}
}

// The second password unlock comes from the in-memory cache (no Argon2id); a wrong password still fails.
func TestUnlockCache(t *testing.T) {
	dir := t.TempDir()
	if err := Create(dir, "lab-master", nil); err != nil {
		t.Fatal(err)
	}
	k1, err := unlockWithPassword(dir, "lab-master")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	k2, err := unlockWithPassword(dir, "lab-master")
	if err != nil || k1 != k2 || time.Since(start) > 50*time.Millisecond {
		t.Fatalf("cached unlock: %v, took %v", err, time.Since(start))
	}
	if _, err := unlockWithPassword(dir, "wrong"); err == nil {
		t.Fatal("wrong password unlocked after a cached unlock")
	}
	if k3, err := unlockWithPassword(dir, "lab-master"); err != nil || k3 != k1 {
		t.Fatalf("unlock after a failed attempt: %v", err)
	}
}
