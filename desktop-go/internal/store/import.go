package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// ImportItem is one session to import: its folder path ("/"-separated, "" = root) and session JSON.
type ImportItem struct {
	Folder  string
	Session map[string]any
}

// ImportSessions adds sessions (new ids), creating their folders by path and reusing existing ones
// with the same name under the same parent (Rust folder_by_path), in one transaction and one write.
func (s *Store) ImportSessions(items []ImportItem) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	cache := map[string]any{}
	n := 0
	for _, it := range items {
		folder, err := folderByPath(tx, it.Folder, cache)
		if err != nil {
			return 0, err
		}
		m := map[string]any{}
		for k, v := range it.Session {
			m[k] = v
		}
		m["id"], m["folder"] = 0, folder
		js, err := json.Marshal(m)
		if err != nil {
			return 0, err
		}
		name, _ := m["name"].(string)
		host, _ := m["host"].(string)
		if _, err := tx.Exec(`INSERT INTO sessions(name, folder, host, json) VALUES(?,?,?,?)`, name, folder, host, string(js)); err != nil {
			return 0, err
		}
		n++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, s.writeBack()
}

// folderByPath returns the id of the folder at path (nil for the root), creating missing levels.
func folderByPath(tx *sql.Tx, path string, cache map[string]any) (any, error) {
	if path == "" {
		return nil, nil
	}
	if id, ok := cache[path]; ok {
		return id, nil
	}
	var parent any
	walked := ""
	for _, name := range strings.Split(path, "/") {
		if walked != "" {
			walked += "/"
		}
		walked += name
		if id, ok := cache[walked]; ok {
			parent = id
			continue
		}
		var id int64
		err := tx.QueryRow(`SELECT id FROM folders WHERE name=? AND parent IS ?`, name, parent).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			res, err := tx.Exec(`INSERT INTO folders(name, parent) VALUES(?,?)`, name, parent)
			if err != nil {
				return nil, err
			}
			id, _ = res.LastInsertId()
		} else if err != nil {
			return nil, err
		}
		cache[walked] = id
		parent = id
	}
	return parent, nil
}
