package store

import (
	"strings"
	"time"
)

// FolderByPath returns the id of the folder at a "/"-separated path (nil for the root), creating the
// missing levels; empty segments are ignored (Rust engine offline ensure_folder).
func (s *Store) FolderByPath(path string) (*int64, error) {
	path = strings.Join(strings.FieldsFunc(path, func(r rune) bool { return r == '/' }), "/")
	if path == "" {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	id, err := folderByPath(tx, path, map[string]any{})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	n := id.(int64)
	return &n, s.writeBack()
}

// AuditRecord is one agent audit row (Rust rxt_store::db::AuditRecord); Session 0 is stored as NULL.
type AuditRecord struct {
	Client, EntryPoint, Command string
	Session                     int64
	Target, Decision, Result    string
}

// AppendAudit writes one audit record, timestamped now.
func (s *Store) AppendAudit(r AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var session any
	if r.Session != 0 {
		session = r.Session
	}
	if _, err := s.db.Exec(
		`INSERT INTO audit (ts, client, entry_point, command, session_id, target, decision, result) VALUES (?,?,?,?,?,?,?,?)`,
		time.Now().Unix(), r.Client, r.EntryPoint, r.Command, session, r.Target, r.Decision, r.Result); err != nil {
		return err
	}
	return s.writeBack()
}
