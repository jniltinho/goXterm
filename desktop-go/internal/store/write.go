package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// writeBack re-encrypts the current plaintext image back to the SQLCipher file. It verifies the
// re-encryption decrypts to the exact same image, keeps a .bak of the previous file, and replaces
// the database atomically, so a crash or a bug never leaves a corrupt store.
func (s *Store) writeBack() error {
	plain, err := os.ReadFile(s.tmp)
	if err != nil {
		return err
	}
	enc, err := encryptDB(plain, s.key, s.salt)
	if err != nil {
		return err
	}

	// Verify before touching the real file: the new ciphertext must decrypt to the same image.
	vpath := filepath.Join(s.dir, dbFile+".verify")
	if err := os.WriteFile(vpath, enc, 0o600); err != nil {
		return err
	}
	defer os.Remove(vpath)
	back, err := decryptDB(vpath, s.key)
	if err != nil {
		return fmt.Errorf("write-back self-check failed: %w", err)
	}
	if !bytes.Equal(back, plain) {
		return errors.New("write-back self-check mismatch; database not changed")
	}

	dbPath := filepath.Join(s.dir, dbFile)
	if cur, err := os.ReadFile(dbPath); err == nil {
		_ = os.WriteFile(dbPath+".bak", cur, 0o600) // best-effort backup of the previous database
	}
	newPath := dbPath + ".new"
	if err := os.WriteFile(newPath, enc, 0o600); err != nil {
		return err
	}
	return os.Rename(newPath, dbPath) // atomic replace on the same filesystem
}

// SaveSession inserts or updates one session from its JSON (the stored session document). id 0 (or a
// json with "id":0) inserts a new row and returns its id; a non-zero id updates that row. name, host
// and folder are taken from the JSON to fill the denormalized columns.
func (s *Store) SaveSession(js string) (int64, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		return 0, fmt.Errorf("invalid session json: %w", err)
	}
	name, _ := m["name"].(string)
	host, _ := m["host"].(string)
	var folder any // NULL unless set
	if f, ok := m["folder"]; ok && f != nil {
		folder = jsonInt(f)
	}
	id := jsonInt(m["id"])

	s.mu.Lock()
	defer s.mu.Unlock()
	if id == 0 {
		res, err := s.db.Exec(`INSERT INTO sessions(name, folder, host, json) VALUES(?,?,?,?)`, name, folder, host, js)
		if err != nil {
			return 0, err
		}
		id, _ = res.LastInsertId()
	} else {
		_, err := s.db.Exec(
			`INSERT INTO sessions(id, name, folder, host, json) VALUES(?,?,?,?,?)
			 ON CONFLICT(id) DO UPDATE SET name=excluded.name, folder=excluded.folder,
			   host=excluded.host, json=excluded.json`,
			id, name, folder, host, js)
		if err != nil {
			return 0, err
		}
	}
	if err := s.writeBack(); err != nil {
		return 0, err
	}
	return id, nil
}

// DeleteSession removes a session and its stored credentials.
func (s *Store) DeleteSession(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`DELETE FROM credentials WHERE session_id=?`, id); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE id=?`, id); err != nil {
		return err
	}
	return s.writeBack()
}

// jsonInt coerces a JSON number (float64) or int to int64.
func jsonInt(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}
