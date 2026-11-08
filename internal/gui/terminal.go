package gui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

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
	cfg := sshconn.Config{
		Host: sv.Host, Port: sv.Port, User: sv.User, Password: password,
		KeyFile: asString(sshOpt["key_file"]), UseAgent: asBool(sshOpt["use_agent"]),
		KeepaliveSecs:         int(asInt64(sshOpt["keepalive_secs"])),
		AgentForwarding:       asBool(sshOpt["agent_forwarding"]),
		NoPassword:            sshOpt["allow_password"] == false,
		NoKeyboardInteractive: sshOpt["allow_keyboard_interactive"] == false,
		X11:                   asBool(sshOpt["x11_forwarding"]),
		RemoteCommand:         asString(sshOpt["remote_command"]),
		StartupDir:            asString(sshOpt["startup_dir"]),
	}
	for _, kv := range asList(sshOpt["env"]) { // [["NAME","value"], …]
		if p := asList(kv); len(p) == 2 {
			cfg.Env = append(cfg.Env, [2]string{asString(p[0]), asString(p[1])})
		}
	}
	cfg.Jumps = a.jumpHosts(sshOpt, cfg.User)
	tabID, err := a.dial(cfg, sessionID, endPolicy{
		remoteCommand: strings.TrimSpace(cfg.RemoteCommand) != "",
		keepOpen:      asBool(sshOpt["keep_open_after_command"]),
		autoReconnect: asBool(sshOpt["auto_reconnect"]),
	})
	if err == nil {
		a.noteTab(tabID, sessionID, sv.Name, sv.Protocol)
	}
	return tabID, err
}

// jumpHosts turns a session's ssh.jump_hosts into gateway hops, each with its own host-key check
// (known_hosts / trust prompt) and password prompt.
func (a *App) jumpHosts(sshOpt map[string]any, targetUser string) []sshconn.Jump {
	var hops []sshconn.Jump
	for _, raw := range asList(sshOpt["jump_hosts"]) {
		j, _ := raw.(map[string]any)
		host := strings.TrimSpace(asString(j["host"]))
		if host == "" {
			continue
		}
		port := int(asInt64(j["port"]))
		if port == 0 {
			port = 22
		}
		user := asString(j["user"])
		if user == "" {
			user = targetUser
		}
		hops = append(hops, sshconn.Jump{
			Host: host, Port: port, User: user, KeyFile: asString(j["key_file"]),
			HostKey:     a.sshHostKeyCallback(host, port),
			AskPassword: a.ask("", host, user, nil),
		})
	}
	return hops
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
	return a.dial(sshconn.Config{Host: host, Port: port, User: user, Password: pass}, 0, endPolicy{})
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
	return a.dial(sshconn.Config{Host: host, Port: port, User: user, Password: password}, 0, endPolicy{})
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
	if a.tails == nil {
		a.tails = map[string]*tail{}
	}
	a.tails[tabID] = &tail{}
	a.mu.Unlock()
	onData := func(b []byte) {
		a.mu.Lock()
		if t := a.tails[tabID]; t != nil {
			t.feed(b)
		}
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
		_ = a.RemoteDisconnect(tabID) // the side SFTP browser rides on this connection
		a.mu.Lock()
		delete(a.shellPID, tabID)
		delete(a.multi.selected, tabID)
		a.mu.Unlock()
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		var end *TermEnd
		var te tabEnd
		if errors.As(err, &te) {
			end = &te.end
		}
		a.mu.Lock()
		delete(a.term, tabID)
		delete(a.tails, tabID)
		delete(a.tabInfo, tabID)
		if e := a.early[tabID]; e != nil {
			e.closed, e.closeMsg, e.end = true, msg, end
			a.mu.Unlock()
			return
		}
		a.mu.Unlock()
		if a.emit != nil {
			a.emit("term:close:"+tabID, map[string]any{"msg": msg, "end": end})
		}
	}
	return tabID, onData, onClose
}

// TermEnd tells the SPA how a terminal ended and what to do next, from the session's SSH options (as
// the Rust engine): close the tab when its remote command exited and "Do not exit after command
// ends" is off; reconnect with backoff after a dropped connection when "Reconnect automatically" is on.
type TermEnd struct {
	Dropped   bool `json:"dropped"`   // the connection was lost (not a normal exit)
	CloseTab  bool `json:"closeTab"`  // close the tab
	Reconnect bool `json:"reconnect"` // reconnect automatically
}

// endPolicy is the part of a session's SSH options that decides TermEnd.
type endPolicy struct{ remoteCommand, keepOpen, autoReconnect bool }

// tabEnd carries the end decision through a stream tab's onClose.
type tabEnd struct {
	err error
	end TermEnd
}

func (e tabEnd) Error() string { return e.err.Error() }
func (e tabEnd) Unwrap() error { return e.err }

// endOf decides how a shell that ended with err is handled.
func (p endPolicy) endOf(err error) TermEnd {
	var st sshconn.ExitStatus
	exited := errors.As(err, &st)
	return TermEnd{Dropped: !exited, CloseTab: exited && p.remoteCommand && !p.keepOpen, Reconnect: !exited && p.autoReconnect}
}

// dial opens an SSH shell tab; sessionID > 0 (a saved session) offers "Save password" at the prompt.
func (a *App) dial(cfg sshconn.Config, sessionID int64, pol endPolicy) (string, error) {
	tabID, onData, closeTab := a.newStreamTab()
	onClose := func(err error) {
		if err == nil {
			closeTab(nil)
			return
		}
		closeTab(tabEnd{err: err, end: pol.endOf(err)})
	}
	ask, saver := a.askSaving(cfg.Host, cfg.User, sessionID)
	cfg.AskPassword = ask
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
	if err := saver.commit(); err != nil { // logged in: keep the password the user chose to save
		onData([]byte("\x1b[90m[rustxterm] password not saved: " + err.Error() + "\x1b[0m\r\n"))
	}
	x11, agentFwd := sess.Forwarding()
	var gw []string
	for _, j := range cfg.Jumps {
		gw = append(gw, j.Host)
	}
	a.setBanner(tabID, a.sshBanner(cfg.User, cfg.Host, cfg.Port, cfg.KeepaliveSecs, sess.ServerVersion(), keyAlgo, keyFP,
		bannerFwd{gateway: strings.Join(gw, " → "), x11: x11, agent: agentFwd}))
	a.mu.Lock()
	a.term[tabID] = sess
	a.mu.Unlock()
	return tabID, nil
}

// Send writes input to a terminal; with MultiExec on, input typed in a selected terminal goes to every
// selected terminal (Rust engine semantics: an unselected terminal only gets its own input).
func (a *App) Send(tabID, data string) error {
	a.mu.Lock()
	targets := a.multi.targets(tabID)
	streams := make([]termStream, 0, len(targets))
	for _, id := range targets {
		if s := a.term[id]; s != nil {
			streams = append(streams, s)
		}
	}
	own := a.term[tabID]
	a.mu.Unlock()
	if own == nil {
		return errors.New("the terminal has closed")
	}
	var first error
	for _, s := range streams {
		if err := s.Write([]byte(data)); err != nil && s == own && first == nil {
			first = err
		}
	}
	return first
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
	_ = a.RemoteDisconnect(tabID) // the tab's side SFTP browser, if open
	a.mu.Lock()
	delete(a.shellPID, tabID)
	delete(a.multi.selected, tabID)
	a.mu.Unlock()
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
