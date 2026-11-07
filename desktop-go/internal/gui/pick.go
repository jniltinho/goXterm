package gui

import "os"

// PickKeyFile opens a native open-file dialog for choosing an SSH private key and returns the chosen
// path (empty when cancelled). The dialog itself is wired in gui.Run (it needs the Wails app).
func (a *App) PickKeyFile() (string, error) {
	if a.pickFile == nil {
		return "", nil
	}
	return a.pickFile()
}

// SaveTerminalOutput asks where to save a terminal's text (native dialog, suggested name) and writes
// it there (0600: terminal output can hold secrets). Returns the path, or "" when cancelled.
func (a *App) SaveTerminalOutput(suggested, text string) (string, error) {
	if a.savePath == nil {
		return "", nil
	}
	path, err := a.savePath(suggested)
	if err != nil || path == "" {
		return "", err
	}
	return path, os.WriteFile(path, []byte(text), 0o600)
}

// Quit closes the application (the Terminal → Exit menu).
func (a *App) Quit() {
	if a.quit != nil {
		a.quit()
	}
}
