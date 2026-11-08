package store

import (
	"database/sql"
	"errors"
)

// Credential kinds, as stored by the Rust app in the credentials table.
const (
	CredPassword   = "password"
	CredPassphrase = "passphrase"
)

// Credential returns the saved secret for a session and kind, as stored by the Rust app (the raw
// UTF-8 bytes of the password/passphrase, protected only by the store encryption). ok is false when
// none is saved. The caller must not log or expose the bytes.
func (s *Store) Credential(sessionID int64, kind string) (secret []byte, ok bool, err error) {
	err = s.db.QueryRow(`SELECT secret FROM credentials WHERE session_id = ? AND kind = ? LIMIT 1`,
		sessionID, kind).Scan(&secret)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return secret, true, nil
}

// SavedCredentialCount counts credential rows (not the secrets), for verification without exposing them.
func (s *Store) SavedCredentialCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM credentials`).Scan(&n)
	return n, err
}

// SetCredential saves (or replaces) a session's secret in the encrypted store, in the Rust layout
// (raw UTF-8 bytes, one row per session and kind).
func (s *Store) SetCredential(sessionID int64, kind string, secret []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO credentials(session_id, kind, secret) VALUES(?,?,?)`,
		sessionID, kind, secret); err != nil {
		return err
	}
	return s.writeBack()
}
