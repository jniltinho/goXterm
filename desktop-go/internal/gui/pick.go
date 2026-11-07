package gui

// PickKeyFile opens a native open-file dialog for choosing an SSH private key and returns the chosen
// path (empty when cancelled). The dialog itself is wired in gui.Run (it needs the Wails app).
func (a *App) PickKeyFile() (string, error) {
	if a.pickFile == nil {
		return "", nil
	}
	return a.pickFile()
}
