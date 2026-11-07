package store

import (
	"database/sql"
	"errors"
	"strings"
)

// SSHTrustKey is one accepted host key for a host:port (base64, as the Rust ssh_trust table stores it).
type SSHTrustKey struct {
	Algorithm string
	KeyBase64 string
}

// SSHTrustKeys returns the host keys the user accepted for host:port, from the database ssh_trust
// table (host matched lowercased, as the Rust app stores it), oldest first.
func (s *Store) SSHTrustKeys(host string, port int) ([]SSHTrustKey, error) {
	rows, err := s.db.Query(`SELECT algorithm, key FROM ssh_trust WHERE host = ? AND port = ? ORDER BY added_at`,
		strings.ToLower(host), port)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SSHTrustKey
	for rows.Next() {
		var k SSHTrustKey
		if err := rows.Scan(&k.Algorithm, &k.KeyBase64); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// CertTrust returns the pinned TLS certificate SHA-256 for host:port (RDP/VNC/FTPS), if any.
func (s *Store) CertTrust(host string, port int) (sha256 string, ok bool, err error) {
	err = s.db.QueryRow(`SELECT sha256 FROM cert_trust WHERE host = ? AND port = ?`,
		strings.ToLower(host), port).Scan(&sha256)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return sha256, true, nil
}

// SaveCertTrust pins a TLS certificate fingerprint for host:port (Rust save_cert_trust).
func (s *Store) SaveCertTrust(host string, port int, sha256 string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO cert_trust (host, port, sha256) VALUES (?, ?, ?)`,
		strings.ToLower(host), port, sha256); err != nil {
		return err
	}
	return s.writeBack()
}

// SSHTrustCount / CertTrustCount report row counts, for verification.
func (s *Store) SSHTrustCount() (int, error)  { return s.count(`ssh_trust`) }
func (s *Store) CertTrustCount() (int, error) { return s.count(`cert_trust`) }

func (s *Store) count(table string) (int, error) {
	var n int
	// table is a fixed literal from this file, never user input.
	err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n)
	return n, err
}
