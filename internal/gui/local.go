package gui

import "rustxterm-go/internal/localconn"

// LocalShell opens a local shell in a PTY and returns its tabId; output streams as "term:out:<tabId>"
// like SSH, so the same Terminal.vue renders it (the "+" / Local terminal tab).
func (a *App) LocalShell() (string, error) {
	tabID, onData, onClose := a.newStreamTab()
	shell := localconn.DefaultShell()
	sess, err := localconn.Start(shell, 80, 24, onData, onClose)
	if err != nil {
		a.dropEarly(tabID)
		return "", err
	}
	a.setBanner(tabID, a.localBanner(shell))
	a.mu.Lock()
	a.term[tabID] = sess
	a.mu.Unlock()
	a.noteTab(tabID, 0, "", "local") // never available to agents
	return tabID, nil
}
