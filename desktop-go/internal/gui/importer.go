package gui

import (
	"errors"

	"rustxterm-go/internal/mobaini"
	"rustxterm-go/internal/store"
)

// ImportPreview is the "Import MobaXterm.ini" dialog's content: the file and what would be imported.
type ImportPreview struct {
	Path   string         `json:"path"`
	Report mobaini.Report `json:"report"`
}

// ImportMobaPick asks for a MobaXterm.ini (native dialog) and returns its preview; nil when cancelled.
// The file is only read, never modified, and its password sections are never read.
func (a *App) ImportMobaPick() (*ImportPreview, error) {
	if a.openPath == nil {
		return nil, errors.New("no file dialog")
	}
	path, err := a.openPath("Import MobaXterm.ini")
	if err != nil || path == "" {
		return nil, err
	}
	r, err := mobaini.ImportFile(path)
	if err != nil {
		return nil, err
	}
	return &ImportPreview{Path: path, Report: r}, nil
}

// ImportMobaApply imports the previewed file's sessions into the store and returns how many.
func (a *App) ImportMobaApply(path string) (int, error) {
	r, err := mobaini.ImportFile(path)
	if err != nil {
		return 0, err
	}
	st, err := openStore()
	if err != nil {
		return 0, err
	}
	defer st.Close()
	items := make([]store.ImportItem, len(r.Sessions))
	for i, it := range r.Sessions {
		items[i] = store.ImportItem{Folder: it.Folder, Session: it.Session}
	}
	return st.ImportSessions(items)
}
