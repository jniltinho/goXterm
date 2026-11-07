package gui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"

	"golang.org/x/crypto/ssh"

	"rustxterm-go/internal/sshconn"
	"rustxterm-go/internal/store"
)

// Connect opens an SSH shell for a saved session and returns a tabId. Output streams to the SPA as
// "term:out:<tabId>" events (base64), and "term:close:<tabId>" when the shell ends.
func (a *App) Connect(sessionID int64) (string, error) {
	st, err := openStore()
	if err != nil {
		return "", err
	}
	rows, err := st.Sessions()
	if err != nil {
		st.Close()
		return "", err
	}
	var sv *SessionView
	var sshOpt map[string]any
	for _, m := range rows {
		if asInt64(m["id"]) == sessionID {
			s := viewOf(m)
			sv = &s
			sshOpt = subMap(m, "ssh")
			break
		}
	}
	if sv == nil {
		st.Close()
		return "", fmt.Errorf("session %d not found", sessionID)
	}
	if sv.Protocol != "ssh" {
		st.Close()
		return "", fmt.Errorf("%s is not wired yet (only SSH)", sv.Protocol)
	}
	var password string
	if secret, ok, _ := st.Credential(sessionID, store.CredPassword); ok {
		password = string(secret)
	}
	st.Close()
	return a.dial(sshconn.Config{
		Host: sv.Host, Port: sv.Port, User: sv.User, Password: password,
		KeyFile: asString(sshOpt["key_file"]), UseAgent: asBool(sshOpt["use_agent"]),
		KeepaliveSecs: int(asInt64(sshOpt["keepalive_secs"])),
	})
}

// HasDemo reports whether a demo SSH target is configured (RXT_DEMO_SSH), used to auto-open a lab
// terminal for screenshots without putting a password in the SPA.
func (a *App) HasDemo() bool { return os.Getenv("RXT_DEMO_SSH") != "" }

// ConnectDemo opens an SSH shell to RXT_DEMO_SSH (`user[:password]@host[:port]`); the password stays
// in Go. Lab/demo only.
func (a *App) ConnectDemo() (string, error) {
	v := os.Getenv("RXT_DEMO_SSH")
	if v == "" {
		return "", errors.New("no demo target")
	}
	user, pass, host, port := parseTarget(v)
	return a.dial(sshconn.Config{Host: host, Port: port, User: user, Password: pass})
}

// parseTarget parses `user[:password]@host[:port]` (port defaults to 22).
func parseTarget(s string) (user, pass, host string, port int) {
	port = 22
	at := -1
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '@' {
			at = i
			break
		}
	}
	creds, rest := "", s
	if at >= 0 {
		creds, rest = s[:at], s[at+1:]
	}
	if c := indexByte(creds, ':'); c >= 0 {
		user, pass = creds[:c], creds[c+1:]
	} else {
		user = creds
	}
	if c := lastIndexByte(rest, ':'); c >= 0 {
		host = rest[:c]
		if p, err := strconv.Atoi(rest[c+1:]); err == nil {
			port = p
		}
	} else {
		host = rest
	}
	return
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
func lastIndexByte(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// QuickConnect opens an SSH shell to host:port as user (used by the quick-connect bar and tests).
func (a *App) QuickConnect(host string, port int, user, password string) (string, error) {
	if port == 0 {
		port = 22
	}
	return a.dial(sshconn.Config{Host: host, Port: port, User: user, Password: password})
}

// newStreamTab allocates a tabId and the output/close callbacks shared by every byte-stream terminal
// (SSH/Telnet/Serial/Local). Until the SPA attaches (TermAttach) output and the close reason are
// buffered; after that output is emitted base64 as "term:out:<tabId>", and "term:close:<tabId>" once.
func (a *App) newStreamTab() (string, func([]byte), func(error)) {
	a.mu.Lock()
	a.seq++
	tabID := "t" + strconv.Itoa(a.seq)
	if a.term == nil {
		a.term = map[string]termStream{}
	}
	if a.early == nil {
		a.early = map[string]*earlyTab{}
	}
	a.early[tabID] = &earlyTab{}
	a.mu.Unlock()
	onData := func(b []byte) {
		a.mu.Lock()
		if e := a.early[tabID]; e != nil {
			if len(e.buf) < earlyCap {
				e.buf = append(e.buf, b...)
			}
			a.mu.Unlock()
			return
		}
		a.mu.Unlock()
		if a.emit != nil {
			a.emit("term:out:"+tabID, base64.StdEncoding.EncodeToString(b))
		}
	}
	onClose := func(err error) {
		a.MonitorStop(tabID)
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		a.mu.Lock()
		delete(a.term, tabID)
		if e := a.early[tabID]; e != nil {
			e.closed, e.closeMsg = true, msg
			a.mu.Unlock()
			return
		}
		a.mu.Unlock()
		if a.emit != nil {
			a.emit("term:close:"+tabID, msg)
		}
	}
	return tabID, onData, onClose
}

func (a *App) dial(cfg sshconn.Config) (string, error) {
	tabID, onData, onClose := a.newStreamTab()
	cfg.AskPassword = a.askPassword(cfg.Host, cfg.User)
	cfg.AskPassphrase = a.askPassphrase
	trust := a.sshHostKeyCallback(cfg.Host, cfg.Port)
	var keyAlgo, keyFP string // recorded for the session banner
	cfg.HostKey = func(h string, r net.Addr, k ssh.PublicKey) error {
		keyAlgo, keyFP = k.Type(), ssh.FingerprintSHA256(k)
		return trust(h, r, k)
	}
	// Surface auth diagnostics as dim lines in the terminal so the user can see whether the key/agent
	// was used before any password prompt.
	cfg.Log = func(s string) { onData([]byte("\x1b[90m[rustxterm] " + s + "\x1b[0m\r\n")) }
	if cfg.Cols == 0 {
		cfg.Cols, cfg.Rows = 80, 24
	}
	sess, err := sshconn.Connect(cfg, onData, onClose)
	if err != nil {
		a.dropEarly(tabID)
		return "", err
	}
	a.setBanner(tabID, a.sshBanner(cfg.User, cfg.Host, cfg.Port, cfg.KeepaliveSecs, sess.ServerVersion(), keyAlgo, keyFP))
	a.mu.Lock()
	a.term[tabID] = sess
	a.mu.Unlock()
	return tabID, nil
}

// Send writes input to a terminal.
func (a *App) Send(tabID, data string) error {
	s := a.session(tabID)
	if s == nil {
		return errors.New("the terminal has closed")
	}
	return s.Write([]byte(data))
}

// Resize changes a terminal's size.
func (a *App) Resize(tabID string, cols, rows int) error {
	s := a.session(tabID)
	if s == nil {
		return nil
	}
	return s.Resize(cols, rows)
}

// Disconnect closes a terminal.
func (a *App) Disconnect(tabID string) error {
	s := a.session(tabID)
	if s == nil {
		return nil
	}
	return s.Close()
}

func (a *App) session(tabID string) termStream {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.term[tabID]
}

// viewOf maps a stored session row to a SessionView (same mapping as Sessions()).
func viewOf(m map[string]any) SessionView {
	return SessionView{
		ID: asInt64(m["id"]), Name: asString(m["name"]), Protocol: asString(m["protocol"]),
		Host: asString(m["host"]), Port: int(asInt64(m["port"])), User: asString(m["user"]),
		Folder: asOptInt64(m["folder"]), Icon: asString(m["icon"]), Password: asString(m["password"]),
	}
}
