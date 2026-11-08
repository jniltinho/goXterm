package store

import (
	"encoding/json"
	"fmt"
)

// Tunnels returns every saved tunnel as its stored JSON document (fields: name, kind, via, bind_addr,
// bind_port, dest_host, dest_port, exposure_confirmed, autostart), ordered by via then name — the same
// tunnels table the Rust app writes.
func (s *Store) Tunnels() ([]map[string]any, error) {
	rows, err := s.db.Query(`SELECT via, name, json FROM tunnels ORDER BY via, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var via int64
		var name, js string
		if err := rows.Scan(&via, &name, &js); err != nil {
			return nil, err
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(js), &m); err != nil {
			return nil, fmt.Errorf("tunnel %d/%s: %w", via, name, err)
		}
		m["via"], m["name"] = via, name // authoritative from the key columns
		out = append(out, m)
	}
	return out, rows.Err()
}

// SaveTunnel inserts or replaces a tunnel from its JSON document, keyed by (via, name) like the Rust
// app. via and name are taken from the JSON to fill the key columns.
func (s *Store) SaveTunnel(js string) error {
	var m map[string]any
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		return fmt.Errorf("invalid tunnel json: %w", err)
	}
	via := jsonInt(m["via"])
	name, _ := m["name"].(string)
	if via == 0 || name == "" {
		return fmt.Errorf("tunnel needs a via session and a name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(
		`INSERT OR REPLACE INTO tunnels(via, name, json) VALUES(?,?,?)`, via, name, js); err != nil {
		return err
	}
	return s.writeBack()
}

// DeleteTunnel removes the tunnel named name on session via.
func (s *Store) DeleteTunnel(via int64, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`DELETE FROM tunnels WHERE via=? AND name=?`, via, name); err != nil {
		return err
	}
	return s.writeBack()
}
