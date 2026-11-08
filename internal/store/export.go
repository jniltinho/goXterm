package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/BurntSushi/toml"
)

// rustXterm TOML export (Rust crates/store/src/export.rs): format + version, then folders, sessions and
// tunnels as the Rust app serializes them, without credentials.
const (
	exportFormat  = "rustxterm-export"
	exportVersion = 1
)

// floatKeys are the session fields Rust reads as floats (f32); every other whole number is an integer.
var floatKeys = map[string]bool{"font_size": true}

// tomlValue drops nulls (Rust None is omitted in TOML) and turns whole JSON numbers into integers.
func tomlValue(key string, v any) (any, bool) {
	switch x := v.(type) {
	case nil:
		return nil, false
	case float64:
		if !floatKeys[key] && x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return int64(x), true
		}
		return x, true
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if c, ok := tomlValue(k, e); ok {
				out[k] = c
			}
		}
		return out, true
	case []any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			if c, ok := tomlValue(key, e); ok {
				out = append(out, c)
			}
		}
		return out, true
	}
	return v, true
}

func tomlTable(m map[string]any) map[string]any {
	t, _ := tomlValue("", m)
	return t.(map[string]any)
}

// ExportTOML writes every folder, session and tunnel (no credentials) as a rustXterm export.
func (s *Store) ExportTOML() (string, error) {
	fs, err := s.Folders()
	if err != nil {
		return "", err
	}
	ss, err := s.Sessions()
	if err != nil {
		return "", err
	}
	ts, err := s.Tunnels()
	if err != nil {
		return "", err
	}
	folders := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		folders = append(folders, tomlTable(f))
	}
	sessions := make([]map[string]any, 0, len(ss))
	for _, m := range ss {
		delete(m, "password") // the "saved"/"absent" marker, never a secret, but not session data
		sessions = append(sessions, tomlTable(m))
	}
	tunnels := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		tunnels = append(tunnels, tomlTable(t))
	}
	var b bytes.Buffer
	b.WriteString(fmt.Sprintf("format = %q\nversion = %d\n\n", exportFormat, exportVersion))
	if err := toml.NewEncoder(&b).Encode(map[string]any{"folders": folders, "sessions": sessions, "tunnels": tunnels}); err != nil {
		return "", err
	}
	return b.String(), nil
}

// Export is the export document as JSON for agents (Rust Export::new): format, version, folders,
// sessions and tunnels, no credentials.
func (s *Store) Export() (map[string]any, error) {
	fs, err := s.Folders()
	if err != nil {
		return nil, err
	}
	ss, err := s.Sessions()
	if err != nil {
		return nil, err
	}
	for _, m := range ss {
		delete(m, "password") // the "saved"/"absent" marker is not session data
	}
	ts, err := s.Tunnels()
	if err != nil {
		return nil, err
	}
	return map[string]any{"format": exportFormat, "version": exportVersion, "folders": fs, "sessions": ss, "tunnels": ts}, nil
}

// credentialKeys never come in from a file (Rust sessions have no such fields).
var credentialKeys = []string{"password", "passphrase", "secret", "credentials"}

// ImportTOML merges a rustXterm export: folders (parents first, reusing same-name folders under the
// same parent), sessions with new ids, tunnels re-pointed at the new sessions. Local-terminal entries
// are skipped. It returns the sessions imported and the local entries skipped.
func (s *Store) ImportTOML(text string) (imported, skippedLocal int, err error) {
	var doc map[string]any
	if _, err := toml.Decode(text, &doc); err != nil {
		return 0, 0, fmt.Errorf("invalid export file: %w", err)
	}
	if doc["format"] != exportFormat {
		return 0, 0, fmt.Errorf("not a rustXterm export (format = %q)", fmt.Sprint(doc["format"]))
	}
	if v, _ := doc["version"].(int64); v != exportVersion {
		return 0, 0, fmt.Errorf("export version %v (supported: %d)", doc["version"], exportVersion)
	}
	tables := func(k string) []map[string]any { t, _ := doc[k].([]map[string]any); return t }

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	folders := tables("folders")
	fids := map[int64]int64{}
	pending := folders
	for len(pending) > 0 { // parents first; folders whose parent never resolves are dropped
		var next []map[string]any
		for _, f := range pending {
			var parent any
			if p, ok := f["parent"].(int64); ok && hasFolder(folders, p) {
				id, done := fids[p]
				if !done {
					next = append(next, f)
					continue
				}
				parent = id
			}
			name, _ := f["name"].(string)
			id, err := findOrCreateFolder(tx, name, parent)
			if err != nil {
				return 0, 0, err
			}
			if icon, ok := f["icon"].(string); ok && icon != "" && icon != "folder" {
				if _, err := tx.Exec(`UPDATE folders SET icon=? WHERE id=?`, icon, id); err != nil {
					return 0, 0, err
				}
			}
			old, _ := f["id"].(int64)
			fids[old] = id
		}
		if len(next) == len(pending) {
			break
		}
		pending = next
	}

	sids := map[int64]int64{}
	for _, m := range tables("sessions") {
		if m["protocol"] == "local" {
			skippedLocal++
			continue
		}
		for _, k := range credentialKeys {
			delete(m, k)
		}
		old, _ := m["id"].(int64)
		var folder any
		if f, ok := m["folder"].(int64); ok {
			if id, ok := fids[f]; ok {
				folder = id
			}
		}
		m["id"], m["folder"] = 0, folder
		js, err := json.Marshal(m)
		if err != nil {
			return 0, 0, err
		}
		name, _ := m["name"].(string)
		host, _ := m["host"].(string)
		res, err := tx.Exec(`INSERT INTO sessions(name, folder, host, json) VALUES(?,?,?,?)`, name, folder, host, string(js))
		if err != nil {
			return 0, 0, err
		}
		id, _ := res.LastInsertId()
		sids[old] = id
		imported++
	}
	for _, t := range tables("tunnels") {
		old, _ := t["via"].(int64)
		via, ok := sids[old]
		if !ok {
			continue // a tunnel's gateway session is not in the file
		}
		t["via"] = via
		name, _ := t["name"].(string)
		js, err := json.Marshal(t)
		if err != nil {
			return 0, 0, err
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO tunnels(via, name, json) VALUES(?,?,?)`, via, name, string(js)); err != nil {
			return 0, 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return imported, skippedLocal, s.writeBack()
}

func hasFolder(fs []map[string]any, id int64) bool {
	for _, f := range fs {
		if f["id"] == id {
			return true
		}
	}
	return false
}

func findOrCreateFolder(tx *sql.Tx, name string, parent any) (int64, error) {
	var id int64
	err := tx.QueryRow(`SELECT id FROM folders WHERE name=? AND parent IS ?`, name, parent).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		res, err := tx.Exec(`INSERT INTO folders(name, parent) VALUES(?,?)`, name, parent)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	return id, err
}
