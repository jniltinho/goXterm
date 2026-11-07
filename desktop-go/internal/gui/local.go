package gui

import "rustxterm-go/internal/localconn"

// LocalShell opens a local shell in a PTY and returns its tabId; output streams as "term:out:<tabId>"
// like SSH, so the same Terminal.vue renders it (the "+" / Local terminal tab).
func (a *App) LocalShell() (string, error) {
	tabID, onData, onClose := a.newStreamTab()
	sess, err := localconn.Start("", 80, 24, onData, onClose)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.term[tabID] = sess
	a.mu.Unlock()
	return tabID, nil
}
