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
	for _, m := range rows {
		if asInt64(m["id"]) == sessionID {
			s := viewOf(m)
			sv = &s
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
	return a.dial(sv.Host, sv.Port, sv.User, password)
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
	return a.dial(host, port, user, pass)
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
	return a.dial(host, port, user, password)
}

// newStreamTab allocates a tabId and the output/close callbacks shared by every byte-stream terminal
// (SSH/Telnet/Serial): output is emitted base64 as "term:out:<tabId>", and "term:close:<tabId>" once.
func (a *App) newStreamTab() (string, func([]byte), func(error)) {
	a.mu.Lock()
	a.seq++
	tabID := "t" + strconv.Itoa(a.seq)
	if a.term == nil {
		a.term = map[string]termStream{}
	}
	a.mu.Unlock()
	onData := func(b []byte) {
		if a.emit != nil {
			a.emit("term:out:"+tabID, base64.StdEncoding.EncodeToString(b))
		}
	}
	onClose := func(err error) {
		a.mu.Lock()
		delete(a.term, tabID)
		a.mu.Unlock()
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		if a.emit != nil {
			a.emit("term:close:"+tabID, msg)
		}
	}
	return tabID, onData, onClose
}

func (a *App) dial(host string, port int, user, password string) (string, error) {
	tabID, onData, onClose := a.newStreamTab()
	sess, err := sshconn.Connect(sshconn.Config{
		Host: host, Port: port, User: user, Password: password,
		HostKey: hostKeyCallback(), Cols: 80, Rows: 24,
	}, onData, onClose)
	if err != nil {
		return "", err
	}
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

// hostKeyCallback verifies against ~/.ssh/known_hosts. RXT_SSH_INSECURE=1 accepts any key (lab tests
// only). A trust-on-first-use prompt and the DB ssh_trust records are a follow-up.
func hostKeyCallback() ssh.HostKeyCallback {
	insecure := os.Getenv("RXT_SSH_INSECURE") == "1"
	known, kherr := store.KnownHostsCallback()
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if insecure {
			return nil
		}
		if kherr == nil {
			if known(hostname, remote, key) == nil {
				return nil
			}
		}
		return fmt.Errorf("host key not trusted for %s (%s)", hostname, ssh.FingerprintSHA256(key))
	}
}

// viewOf maps a stored session row to a SessionView (same mapping as Sessions()).
func viewOf(m map[string]any) SessionView {
	return SessionView{
		ID: asInt64(m["id"]), Name: asString(m["name"]), Protocol: asString(m["protocol"]),
		Host: asString(m["host"]), Port: int(asInt64(m["port"])), User: asString(m["user"]),
		Folder: asOptInt64(m["folder"]), Icon: asString(m["icon"]), Password: asString(m["password"]),
	}
}
