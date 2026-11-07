package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite" // pure-Go SQLite (no cgo); reads the decrypted plaintext image
)

const dbFile = "rustxterm.db"

// Store is an open rustXterm database. The SQLCipher file is decrypted in pure Go (sqlcipher.go) into
// a short-lived plaintext image opened with modernc.org/sqlite (no cgo); writes go to that image and
// are re-encrypted back to the SQLCipher file by writeBack (see write.go). The plaintext temp holds
// credentials, so it is 0600 and removed on Close.
type Store struct {
	db   *sql.DB
	tmp  string
	dir  string   // directory holding rustxterm.db (for write-back)
	key  [32]byte // raw SQLCipher key (for re-encryption on write-back)
	salt []byte   // original SQLCipher salt, reused so the same key keeps decrypting
	mu   sync.Mutex
}

// Open unlocks the envelope in dir with the master password and opens dir/rustxterm.db.
func Open(dir, password string) (*Store, error) {
	key, err := unlockWithPassword(dir, password)
	if err != nil {
		return nil, err
	}
	return openDB(dir, key)
}

// OpenWithKeyring opens silently through the keyring wrap; ok=false when there is none.
func OpenWithKeyring(dir string, kr keyring) (*Store, bool, error) {
	key, ok, err := unlockWithKeyring(dir, kr)
	if err != nil || !ok {
		return nil, false, err
	}
	s, err := openDB(dir, key)
	return s, err == nil, err
}

func openDB(dir string, key dbKey) (*Store, error) {
	dbPath := filepath.Join(dir, dbFile)
	plain, err := decryptDB(dbPath, [32]byte(key))
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp("", "rxt-store-*.db")
	if err != nil {
		return nil, err
	}
	tmp := f.Name()
	_ = f.Chmod(0o600)
	if _, err := f.Write(plain); err != nil {
		f.Close()
		os.Remove(tmp)
		return nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	// Open read-write (a rollback journal, not WAL, so committed writes land in the main temp file
	// that writeBack re-encrypts); the SQLCipher file itself is only touched by writeBack.
	db, err := sql.Open("sqlite", "file:"+tmp+"?_pragma=journal_mode(DELETE)&_pragma=foreign_keys(1)")
	if err != nil {
		os.Remove(tmp)
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		os.Remove(tmp)
		return nil, errors.New("wrong master password or not a rustXterm database")
	}
	return &Store{db: db, tmp: tmp, dir: dir, key: [32]byte(key), salt: raw[:saltSize]}, nil
}

// Close releases the database and removes the decrypted temp file.
func (s *Store) Close() error {
	var err error
	if s.db != nil {
		err = s.db.Close()
		s.db = nil
	}
	if s.tmp != "" {
		_ = os.Remove(s.tmp)
		s.tmp = ""
	}
	return err
}

// Sessions returns every saved session as the agent JSON the Rust CLI prints: the stored session
// JSON with id and folder taken from the row and a "password" field ("saved"/"absent").
func (s *Store) Sessions() ([]map[string]any, error) {
	rows, err := s.db.Query(`SELECT id, folder, json FROM sessions ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int64
		var folder sql.NullInt64
		var js string
		if err := rows.Scan(&id, &folder, &js); err != nil {
			return nil, err
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(js), &m); err != nil {
			return nil, fmt.Errorf("session %d: %w", id, err)
		}
		m["id"] = id
		if folder.Valid {
			m["folder"] = folder.Int64
		} else {
			m["folder"] = nil
		}
		has, err := s.hasPassword(id)
		if err != nil {
			return nil, err
		}
		if has {
			m["password"] = "saved"
		} else {
			m["password"] = "absent"
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Folders returns every folder (id, name, parent, icon).
func (s *Store) Folders() ([]map[string]any, error) {
	rows, err := s.db.Query(`SELECT id, name, parent, icon FROM folders ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int64
		var name string
		var parent sql.NullInt64
		var icon sql.NullString
		if err := rows.Scan(&id, &name, &parent, &icon); err != nil {
			return nil, err
		}
		f := map[string]any{"id": id, "name": name, "parent": nil, "icon": nil}
		if parent.Valid {
			f["parent"] = parent.Int64
		}
		if icon.Valid {
			f["icon"] = icon.String
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) hasPassword(id int64) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM credentials WHERE session_id = ? AND kind = 'password' LIMIT 1`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
