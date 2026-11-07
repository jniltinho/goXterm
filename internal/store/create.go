package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

// Rust crates/store/src/db.rs MIGRATIONS (append only, never edit): user_version = how many ran.
var migrations = []string{`
CREATE TABLE folders (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    parent INTEGER REFERENCES folders(id) ON DELETE SET NULL
);
CREATE TABLE sessions (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    folder INTEGER REFERENCES folders(id) ON DELETE SET NULL,
    host TEXT NOT NULL,
    json TEXT NOT NULL
);
CREATE TABLE tunnels (
    via INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    json TEXT NOT NULL,
    PRIMARY KEY (via, name)
);
CREATE TABLE ssh_trust (
    host TEXT NOT NULL,
    port INTEGER NOT NULL,
    algorithm TEXT NOT NULL,
    key TEXT NOT NULL,
    added_at INTEGER NOT NULL,
    PRIMARY KEY (host, port, algorithm, key)
);
CREATE TABLE cert_trust (
    host TEXT NOT NULL,
    port INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    PRIMARY KEY (host, port)
);
CREATE TABLE credentials (
    session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    secret BLOB NOT NULL,
    PRIMARY KEY (session_id, kind)
);
CREATE TABLE policies (
    session_id INTEGER PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    policy TEXT NOT NULL,
    content_read INTEGER NOT NULL
);
CREATE TABLE audit (
    id INTEGER PRIMARY KEY,
    ts INTEGER NOT NULL,
    client TEXT NOT NULL,
    entry_point TEXT NOT NULL,
    command TEXT NOT NULL,
    session_id INTEGER,
    target TEXT NOT NULL,
    decision TEXT NOT NULL,
    result TEXT NOT NULL
);
CREATE TABLE history (
    id INTEGER PRIMARY KEY,
    ts INTEGER NOT NULL,
    session_id INTEGER,
    action TEXT NOT NULL
);
`, "ALTER TABLE folders ADD COLUMN icon TEXT;"}

// Rust KdfParams::DEFAULT (256 MiB, 3 passes, 1 lane).
const kdfMKiB, kdfT, kdfP = 256 * 1024, 3, 1

// Exists reports whether dir already holds a store (first start = no key envelope).
func Exists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, envelopeFile))
	return err == nil
}

// Create is the first start (Rust envelope::create + Db::open): a new random database key wrapped
// under the master password in keys.v1, and an empty SQLCipher database with the full schema. It
// refuses to replace an existing envelope. With kr (Settings "Unlock with system keyring") a second
// wrap opens it through a random key kept in the platform keyring; a keyring error aborts before
// anything is written. The store is then opened with Open or OpenWithKeyring.
func Create(dir, password string, kr keyring) error {
	if password == "" {
		return errors.New("the master password cannot be empty")
	}
	if Exists(dir) {
		return errors.New("a store already exists here")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return err
	}
	var krWrap map[string]any
	if kr != nil {
		w, err := keyringWrap(kr, key)
		if err != nil {
			return fmt.Errorf("system keyring: %w", err)
		}
		krWrap = w
	}
	image, err := emptyImage()
	if err != nil {
		return err
	}
	dbSalt := make([]byte, saltSize)
	if _, err := rand.Read(dbSalt); err != nil {
		return err
	}
	enc, err := encryptDB(image, key, dbSalt)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, dbFile), enc, 0o600); err != nil {
		return err
	}
	return writeEnvelope(dir, password, key, krWrap) // last: the store exists only once both files do
}

// emptyImage builds the plaintext SQLite image: 4096-byte pages with the 80 reserved bytes per page
// SQLCipher keeps for the IV and HMAC (set in the header before any table exists), then the schema.
func emptyImage() ([]byte, error) {
	f, err := os.CreateTemp("", "rxt-new-*.db")
	if err != nil {
		return nil, err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	_ = f.Chmod(0o600)
	page := make([]byte, pageSize)
	copy(page, sqliteHeader)
	h := page[16:]
	h[0], h[1] = pageSize>>8, pageSize&0xff                                           // page size
	h[2], h[3] = 1, 1                                                                 // legacy file format (rollback journal)
	h[4] = reserve                                                                    // reserved bytes per page
	h[5], h[6], h[7] = 64, 32, 32                                                     // payload fractions
	page[31] = 1                                                                      // database size: 1 page (bytes 28..31, big endian)
	page[47] = 4                                                                      // schema format 4 (bytes 44..47)
	page[59] = 1                                                                      // text encoding UTF-8 (bytes 56..59)
	page[100] = 0x0d                                                                  // page 1: empty table-leaf b-tree
	page[105], page[106] = byte((pageSize-reserve)>>8), byte((pageSize-reserve)&0xff) // cell content start
	if _, err := f.Write(page); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+tmp+"?_pragma=journal_mode(DELETE)")
	if err != nil {
		return nil, err
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			db.Close()
			return nil, fmt.Errorf("schema: %w", err)
		}
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", len(migrations))); err != nil {
		db.Close()
		return nil, err
	}
	if err := db.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(tmp)
}

// writeEnvelope writes keys.v1 with the master wrap (Rust envelope::create_with_params), atomically.
func writeEnvelope(dir, password string, key [32]byte, krWrap map[string]any) error {
	salt := make([]byte, 16)
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	wrapping := argon2.IDKey([]byte(password), salt, kdfT, kdfMKiB, kdfP, 32)
	aead, err := chacha20poly1305.NewX(wrapping)
	if err != nil {
		return err
	}
	ct := aead.Seal(nil, nonce, key[:], []byte(aadMaster))
	b64 := base64.StdEncoding.EncodeToString
	wraps := []map[string]any{{"kind": "master", "nonce": b64(nonce), "ct": b64(ct)}}
	if krWrap != nil {
		wraps = append(wraps, krWrap)
	}
	env := map[string]any{
		"version": 1,
		"kdf":     map[string]any{"alg": "argon2id", "m_kib": kdfMKiB, "t": kdfT, "p": kdfP, "salt": b64(salt)},
		"wraps":   wraps,
	}
	js, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, envelopeFile+".tmp")
	if err := os.WriteFile(tmp, js, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, envelopeFile))
}

// keyringWrap stores a new random wrapping key in the keyring under "rustxterm-db-<32 hex>" (Rust
// KEYRING_ID_PREFIX) and seals the database key with it.
func keyringWrap(kr keyring, key [32]byte) (map[string]any, error) {
	idb := make([]byte, 16)
	wrapping := make([]byte, 32)
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	for _, b := range [][]byte{idb, wrapping, nonce} {
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
	}
	id := fmt.Sprintf("rustxterm-db-%x", idb)
	if err := kr.set(id, wrapping); err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.NewX(wrapping)
	if err != nil {
		return nil, err
	}
	b64 := base64.StdEncoding.EncodeToString
	ct := aead.Seal(nil, nonce, key[:], []byte(aadKeyring))
	return map[string]any{"kind": "keyring", "id": id, "nonce": b64(nonce), "ct": b64(ct)}, nil
}
