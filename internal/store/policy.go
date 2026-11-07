package store

import "database/sql"

// Policy is a session's agent policy (Rust rxt_store::db::Policy): "deny" | "ask" | "allow" plus the
// separate content-read flag. A session without a row is deny with content-read off.
type Policy struct {
	Policy      string `json:"policy"`
	ContentRead bool   `json:"content_read"`
}

func normPolicy(p string) string {
	if p == "ask" || p == "allow" {
		return p
	}
	return "deny" // unknown values fail closed, as in Rust parse_policy
}

// Policy reads a session's agent policy.
func (s *Store) Policy(session int64) (Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var p Policy
	err := s.db.QueryRow(`SELECT policy, content_read FROM policies WHERE session_id=?`, session).Scan(&p.Policy, &p.ContentRead)
	if err == sql.ErrNoRows {
		return Policy{Policy: "deny"}, nil
	}
	p.Policy = normPolicy(p.Policy)
	return p, err
}

// SetPolicy stores a session's agent policy.
func (s *Store) SetPolicy(session int64, p Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO policies(session_id, policy, content_read) VALUES(?,?,?)`,
		session, normPolicy(p.Policy), p.ContentRead); err != nil {
		return err
	}
	return s.writeBack()
}

// KnownUsers lists the distinct user names of the saved sessions (the editor's user picker).
func (s *Store) KnownUsers() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT DISTINCT json_extract(json, '$.user') AS u FROM sessions
		WHERE coalesce(u, '') <> '' ORDER BY u COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
