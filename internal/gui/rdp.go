package gui

import (
	"encoding/base64"
	"errors"
	"os"
	"strconv"
	"strings"

	"rustxterm-go/internal/rdpconn"
)

// RdpView is returned on connect: the tab id and requested desktop geometry.
type RdpView struct {
	TabID  string `json:"tabId"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// RdpConnect opens an RDP session for a saved session, honoring its stored RDP options (domain,
// allow_legacy_tls, allow_standard_security). Security mapping for pure Go: standard security → Std
// (which falls back to the CredSSP-or-TLS offer: standard RDP security itself is not implemented, see
// rdpconn.Std); legacy TLS (old 2008-era servers) → SSL, because NLA over TLS 1.0 is unreachable on
// Go's crypto/tls (see rdpconn.Config.LegacyTLS); otherwise NLA over TLS 1.2+.
func (a *App) RdpConnect(sessionID int64, tabW, tabH int) (*RdpView, error) {
	m, password, err := sessionRawByID(sessionID)
	if err != nil {
		return nil, err
	}
	if asString(m["protocol"]) != "rdp" {
		return nil, errors.New("not an RDP session")
	}
	rdp := subMap(m, "rdp")
	legacy := asBool(rdp["allow_legacy_tls"])
	proto := rdpconn.NLA
	if asBool(rdp["allow_standard_security"]) {
		proto = rdpconn.Std
	} else if legacy {
		proto = rdpconn.SSL
	}
	port := int(asInt64(m["port"]))
	if port == 0 {
		port = 3389
	}
	user := asString(m["user"])
	if d := asString(rdp["domain"]); d != "" && !strings.ContainsAny(user, `\/@`) {
		user = d + `\` + user // NLA splits DOMAIN\user and user@domain
	}
	w, h := rdpSize(rdp["resolution"], tabW, tabH)
	// No saved password: ask (with "Save password"). It travels only inside the TLS channel, after the
	// certificate check, and is kept only once the connection is up.
	var saver *passwordSaver
	if password == "" {
		saver = &passwordSaver{sessionID: sessionID}
		pw, err := a.ask("", asString(m["host"]), user, saver)()
		if err != nil {
			return nil, err
		}
		password = pw
	}
	view, err := a.rdpDial(rdpconn.Config{
		Host: asString(m["host"]), Port: port, User: user, Password: password,
		Width: w, Height: h, Protocol: proto, LegacyTLS: legacy,
		KeyboardLayout: uint32(asInt64(rdp["keyboard_layout"])), // #nosec G115 -- a locale id from the editor list
		VerifyTLS:      a.tlsVerifier(asString(m["host"]), port),
	}, clipboardOn(rdp))
	if err == nil && saver != nil {
		a.mu.Lock()
		if a.rdpSave == nil {
			a.rdpSave = map[string]*passwordSaver{}
		}
		a.rdpSave[view.TabID] = saver // stored by RdpStart once the login worked
		a.mu.Unlock()
	}
	return view, err
}

// rdpSize picks the remote desktop size like the Rust app: a session's fixed resolution
// ({"fixed":{"width","height"}}), otherwise the tab's size ("fittotab", the default), clamped to
// what RDP servers accept, width rounded down to a multiple of 4 (some servers misdraw otherwise).
func rdpSize(resolution any, tabW, tabH int) (int, int) {
	if m, ok := resolution.(map[string]any); ok {
		if f, ok := m["fixed"].(map[string]any); ok {
			if w, h := int(asInt64(f["width"])), int(asInt64(f["height"])); w > 0 && h > 0 {
				return w, h
			}
		}
	}
	if tabW <= 0 || tabH <= 0 {
		return 1280, 800 // the tab was not laid out yet
	}
	clamp := func(v, lo, hi int) int { return min(max(v, lo), hi) }
	return clamp(tabW, 640, 4096) &^ 3, clamp(tabH, 480, 2048)
}

// HasRdpDemo / ConnectRdpDemo auto-open an RDP view to RXT_DEMO_RDP = "[proto ]user:pass@host:port"
// (proto: ssl|nla|std; default ssl). Lab/demo only.
func (a *App) HasRdpDemo() bool { return os.Getenv("RXT_DEMO_RDP") != "" }

func (a *App) ConnectRdpDemo(tabW, tabH int) (*RdpView, error) {
	v := os.Getenv("RXT_DEMO_RDP")
	if v == "" {
		return nil, errors.New("no demo target")
	}
	proto := rdpconn.SSL
	if sp := strings.IndexByte(v, ' '); sp >= 0 {
		switch v[:sp] {
		case "nla":
			proto = rdpconn.NLA
		case "std":
			proto = rdpconn.Std
		}
		v = strings.TrimSpace(v[sp+1:])
	}
	user, pass, host, port := parseTarget(v)
	if port == 0 {
		port = 3389
	}
	w, h := rdpSize(nil, tabW, tabH)
	return a.rdpDial(rdpconn.Config{
		Host: host, Port: port, User: user, Password: pass, Width: w, Height: h,
		Protocol: proto, LegacyTLS: proto != rdpconn.NLA, // ssl/std demos target old servers
	}, true)
}

// clipboardOn reads "Share clipboard text" (default on, as in the Rust app).
func clipboardOn(opts map[string]any) bool {
	v, set := opts["clipboard"]
	return !set || asBool(v)
}

func (a *App) rdpDial(cfg rdpconn.Config, clipboard bool) (*RdpView, error) {
	if cfg.Width == 0 {
		cfg.Width, cfg.Height = 1280, 800
	}
	a.mu.Lock()
	a.seq++
	tabID := "r" + strconv.Itoa(a.seq)
	a.mu.Unlock()

	onFrame := func(r rdpconn.Rect) {
		if a.emit != nil {
			a.emit("rdp:frame:"+tabID, map[string]any{
				"x": r.X, "y": r.Y, "png": base64.StdEncoding.EncodeToString(r.PNG),
			})
		}
	}
	onReady := func() {
		if a.emit != nil {
			a.emit("rdp:ready:"+tabID, "")
		}
	}
	onClose := func(err error) {
		a.mu.Lock()
		delete(a.rdp, tabID)
		a.mu.Unlock()
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		if a.emit != nil {
			a.emit("rdp:close:"+tabID, msg)
		}
	}
	if clipboard {
		cfg.OnClipboard = func(text string) { // copied on the remote desktop → the local clipboard
			if a.clip != nil {
				a.clip.SetText(text)
			}
		}
	}
	sess, w, h, err := rdpconn.Connect(cfg, onFrame, onReady, onClose)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	if a.rdp == nil {
		a.rdp = map[string]*rdpconn.Session{}
	}
	a.rdp[tabID] = sess
	a.mu.Unlock()
	return &RdpView{TabID: tabID, Width: w, Height: h}, nil
}

// RdpStart logs in and begins streaming (call after subscribing to rdp:frame events).
func (a *App) RdpStart(tabID string) error {
	s := a.rdpSession(tabID)
	if s == nil {
		return errors.New("the RDP session has closed")
	}
	if err := s.Start(); err != nil {
		return err
	}
	a.mu.Lock()
	saver := a.rdpSave[tabID]
	delete(a.rdpSave, tabID)
	a.mu.Unlock()
	if saver != nil {
		_ = saver.commit() // logged in: keep the password the user chose to save
	}
	return nil
}

// RdpMouse sends a mouse event: kind "move"/"down"/"up"/"wheel"; button 0 left,1 middle,2 right.
func (a *App) RdpMouse(tabID, kind string, button, x, y int) error {
	s := a.rdpSession(tabID)
	if s == nil {
		return nil
	}
	switch kind {
	case "move":
		s.MouseMove(x, y)
	case "down":
		s.MouseDown(button, x, y)
	case "up":
		s.MouseUp(button, x, y)
	case "wheel":
		s.Wheel(button, x, y) // button carries the scroll delta here
	}
	return nil
}

// RdpKey sends a scancode key event.
// RdpSyncClipboard offers the local clipboard's text to the remote desktop (the SPA calls it when the
// RDP view gets focus, so text copied locally can be pasted there).
func (a *App) RdpSyncClipboard(tabID string) error {
	s := a.rdpSession(tabID)
	if s == nil || a.clip == nil {
		return nil
	}
	text, ok := a.clip.Text()
	if !ok || text == "" {
		return nil
	}
	return s.SetClipboard(text)
}

func (a *App) RdpKey(tabID string, down bool, scancode int) error {
	s := a.rdpSession(tabID)
	if s == nil {
		return nil
	}
	if down {
		s.KeyDown(scancode)
	} else {
		s.KeyUp(scancode)
	}
	return nil
}

// RdpDisconnect closes an RDP session.
func (a *App) RdpDisconnect(tabID string) error {
	a.mu.Lock()
	s := a.rdp[tabID]
	delete(a.rdp, tabID)
	a.mu.Unlock()
	if s == nil {
		return nil
	}
	return s.Close()
}

func (a *App) rdpSession(tabID string) *rdpconn.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rdp[tabID]
}
