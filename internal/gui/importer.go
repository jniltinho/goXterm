package gui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
	path, err := a.openPath("Import MobaXterm.ini", mobaDir())
	if err != nil || path == "" {
		return nil, err
	}
	r, err := mobaini.ImportFile(path)
	if err != nil {
		return nil, err
	}
	return &ImportPreview{Path: path, Report: r}, nil
}

// mobaDir is where the installed MobaXterm keeps MobaXterm.ini on Windows (%APPDATA%\MobaXterm, a hidden folder),
// or "" when there is none (other systems, the portable edition keeps it next to its .exe).
func mobaDir() string {
	if appData := os.Getenv("APPDATA"); appData != "" {
		if dir := filepath.Join(appData, "MobaXterm"); isDir(dir) {
			return dir
		}
	}
	return ""
}

func isDir(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }

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

// ExportPlan is the "Export sessions" confirmation: where, and how much is written.
type ExportPlan struct {
	Path     string `json:"path"`
	Sessions int    `json:"sessions"`
	Folders  int    `json:"folders"`
	Tunnels  int    `json:"tunnels"`
}

// ExportTOMLPick asks where to save the rustXterm export (native dialog); nil when cancelled.
func (a *App) ExportTOMLPick() (*ExportPlan, error) {
	if a.savePath == nil {
		return nil, errors.New("no file dialog")
	}
	path, err := a.savePath("rustxterm-sessions.toml")
	if err != nil || path == "" {
		return nil, err
	}
	st, err := openStore()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	ss, _ := st.Sessions()
	fs, _ := st.Folders()
	ts, _ := st.Tunnels()
	return &ExportPlan{Path: path, Sessions: len(ss), Folders: len(fs), Tunnels: len(ts)}, nil
}

// ExportTOMLWrite writes the export (no passwords, passphrases or trust records) to path.
func (a *App) ExportTOMLWrite(path string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	text, err := st.ExportTOML()
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o600)
}

// ImportTOMLPick asks for a rustXterm export and merges it; returns the status line ("" if cancelled).
func (a *App) ImportTOMLPick() (string, error) {
	if a.openPath == nil {
		return "", errors.New("no file dialog")
	}
	path, err := a.openPath("Import rustXterm sessions", "")
	if err != nil || path == "" {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	st, err := openStore()
	if err != nil {
		return "", err
	}
	defer st.Close()
	n, local, err := st.ImportTOML(string(b))
	if err != nil {
		return "", fmt.Errorf("cannot import %s: %w", path, err)
	}
	msg := fmt.Sprintf("Imported %d session(s) from %s.", n, path)
	if local > 0 {
		msg += fmt.Sprintf(" Skipped %d local terminal entry(ies): they are never saved.", local)
	}
	return msg, nil
}
