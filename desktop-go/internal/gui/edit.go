package gui

import (
	"encoding/json"
)

// SaveSession inserts or updates a session from its JSON document (the Rust session shape) and
// returns its id. The store re-encrypts the SQLCipher database with a backup and a self-check. Any
// "password" field is stripped before saving — the session document never holds secrets (saving the
// password as a credential is a follow-up).
func (a *App) SaveSession(js string) (int64, error) {
	clean, err := stripPassword(js)
	if err != nil {
		return 0, err
	}
	st, err := openStore()
	if err != nil {
		return 0, err
	}
	defer st.Close()
	return st.SaveSession(clean)
}

// DeleteSession removes a saved session and its credentials.
func (a *App) DeleteSession(id int64) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	return st.DeleteSession(id)
}

// DuplicateSession copies a session (new id, name + " (copy)") and returns the new id.
func (a *App) DuplicateSession(id int64) (int64, error) {
	m, _, err := sessionRawByID(id)
	if err != nil {
		return 0, err
	}
	m["id"] = int64(0)
	m["name"] = asString(m["name"]) + " (copy)"
	delete(m, "password")
	b, err := json.Marshal(m)
	if err != nil {
		return 0, err
	}
	return a.SaveSession(string(b))
}

// RenameSession changes a session's name, keeping all other fields.
func (a *App) RenameSession(id int64, name string) error {
	m, _, err := sessionRawByID(id)
	if err != nil {
		return err
	}
	m["name"] = name
	delete(m, "password")
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = a.SaveSession(string(b))
	return err
}

// SaveFolder inserts or updates a tree folder (id 0 = new) and returns its id.
func (a *App) SaveFolder(id int64, name string, parent *int64) (int64, error) {
	st, err := openStore()
	if err != nil {
		return 0, err
	}
	defer st.Close()
	return st.SaveFolder(id, name, parent)
}

// SetFolderIcon sets a folder's icon (Change color / Edit folder); "" or "folder" = default yellow.
func (a *App) SetFolderIcon(id int64, icon string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	return st.SetFolderIcon(id, icon)
}

// MoveSession moves a session to a folder (nil = root; the tree's "Move to root").
func (a *App) MoveSession(id int64, folder *int64) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	return st.MoveSession(id, folder)
}

// DeleteFolder removes a folder; its sessions and subfolders move to its parent.
func (a *App) DeleteFolder(id int64) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	return st.DeleteFolder(id)
}

// SessionJSON returns the stored JSON for a session so the editor can load every field (including
// per-protocol options), with the transient "password" marker removed.
func (a *App) SessionJSON(id int64) (string, error) {
	m, _, err := sessionRawByID(id)
	if err != nil {
		return "", err
	}
	delete(m, "password")
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// stripPassword removes any top-level "password" field so no secret is written into the stored
// session document.
func stripPassword(js string) (string, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		return "", err
	}
	if _, ok := m["password"]; !ok {
		return js, nil
	}
	delete(m, "password")
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
