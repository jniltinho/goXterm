package gui

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"rustxterm-go/internal/store"
)

// passAns is the user's answer to an interactive password prompt.
type passAns struct {
	pw   string
	ok   bool
	save bool
}

// passwordSaver keeps the password a saved session's prompt was answered with while "Save password"
// was ticked (Rust dialogs.rs), so it is stored only once the login succeeds.
type passwordSaver struct {
	sessionID int64
	mu        sync.Mutex
	pw        string
	save      bool
}

func (p *passwordSaver) answered(ans passAns) {
	p.mu.Lock()
	p.pw, p.save = ans.pw, ans.save
	p.mu.Unlock()
}

// commit stores the ticked password in the encrypted store (after a successful login).
func (p *passwordSaver) commit() error {
	p.mu.Lock()
	pw, save := p.pw, p.save
	p.mu.Unlock()
	if !save || pw == "" {
		return nil
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	return st.SetCredential(p.sessionID, store.CredPassword, []byte(pw))
}

// askPassword returns a callback (for sshconn.Config.AskPassword) that prompts the user for the
// account password through the SPA ("ssh:password") and blocks until SshPasswordRespond delivers it.
func (a *App) askPassword(host, user string) func() (string, error) {
	return a.ask("", host, user, nil)
}

// askSaving is askPassword for a saved session: the prompt offers "Save password" and the returned
// saver stores the typed password once the caller's login succeeds.
func (a *App) askSaving(host, user string, sessionID int64) (func() (string, error), *passwordSaver) {
	p := &passwordSaver{sessionID: sessionID}
	return a.ask("", host, user, p), p
}

// askPassphrase prompts for an SSH key file's passphrase, with wording that distinguishes it from the
// account password (used by sshconn when a key is passphrase-protected).
func (a *App) askPassphrase(keyFile string) (string, error) {
	return a.ask("Enter the passphrase for key "+keyFile, "", "", nil)()
}

// ask emits an "ssh:password" prompt and blocks for the answer. prompt overrides the default dialog
// text (account password) when non-empty — e.g. a key passphrase.
func (a *App) ask(prompt, host, user string, saver *passwordSaver) func() (string, error) {
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
			a.emit("ssh:password", map[string]any{"reqId": reqID, "host": host, "user": user, "prompt": prompt,
				"canSave": saver != nil && saver.sessionID > 0})
		}
		select {
		case ans := <-ch:
			if !ans.ok {
				return "", fmt.Errorf("password entry cancelled")
			}
			if saver != nil {
				saver.answered(ans)
			}
			return ans.pw, nil
		case <-time.After(2 * time.Minute):
			return "", fmt.Errorf("password prompt timed out")
		}
	}
}

// SshPasswordRespond delivers the password the user typed (ok=false when cancelled) for a pending
// ssh:password prompt; save is the prompt's "Save password" box.
func (a *App) SshPasswordRespond(reqID, password string, ok, save bool) {
	a.mu.Lock()
	ch := a.pass[reqID]
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- passAns{password, ok, save}:
		default:
		}
	}
}
