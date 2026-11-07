package gui

import (
	"fmt"
	"strconv"
	"time"
)

// passAns is the user's answer to an interactive password prompt.
type passAns struct {
	pw string
	ok bool
}

// askPassword returns a callback (for sshconn.Config.AskPassword) that prompts the user for a
// password through the SPA ("ssh:password") and blocks until SshPasswordRespond delivers it. Used for
// sessions with no saved credential, or when a saved password was rejected.
func (a *App) askPassword(host, user string) func() (string, error) {
	return func() (string, error) {
		ch := make(chan passAns, 1)
		a.mu.Lock()
		a.seq++
		reqID := "pass" + strconv.Itoa(a.seq)
		if a.pass == nil {
			a.pass = map[string]chan passAns{}
		}
		a.pass[reqID] = ch
		a.mu.Unlock()
		defer func() {
			a.mu.Lock()
			delete(a.pass, reqID)
			a.mu.Unlock()
		}()

		if a.emit != nil {
			a.emit("ssh:password", map[string]any{"reqId": reqID, "host": host, "user": user})
		}
		select {
		case ans := <-ch:
			if !ans.ok {
				return "", fmt.Errorf("password entry cancelled")
			}
			return ans.pw, nil
		case <-time.After(2 * time.Minute):
			return "", fmt.Errorf("password prompt timed out")
		}
	}
}

// SshPasswordRespond delivers the password the user typed (ok=false when cancelled) for a pending
// ssh:password prompt.
func (a *App) SshPasswordRespond(reqID, password string, ok bool) {
	a.mu.Lock()
	ch := a.pass[reqID]
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- passAns{password, ok}:
		default:
		}
	}
}
