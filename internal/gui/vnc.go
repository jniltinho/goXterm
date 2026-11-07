package gui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"rustxterm-go/internal/sshconn"
	"rustxterm-go/internal/vncconn"
)

// VncView is returned on connect: the tab id and desktop geometry for sizing the canvas.
type VncView struct {
	TabID  string `json:"tabId"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Name   string `json:"name"`
	// ViewOnly: the session sends no pointer or key events (the view also ignores them).
	ViewOnly bool `json:"viewOnly"`
}

// vncTarget is how a VNC session reaches its server: an optional SSH gateway (direct-tcpip on that
// connection, no local port) and whether input is sent.
type vncTarget struct {
	dial     func(network, addr string) (net.Conn, error)
	done     func() // closes the gateway connection with the session
	viewOnly bool
}

// vncGateway opens the session's SSH gateway ("Connect through an SSH gateway"), with the same host
// key trust and password prompt as an SSH tab.
func (a *App) vncGateway(via map[string]any) (*vncTarget, error) {
	host, user := asString(via["host"]), asString(via["user"])
	port := int(asInt64(via["port"]))
	if port == 0 {
		port = 22
	}
	client, err := sshconn.DialClient(sshconn.Config{
		Host: host, Port: port, User: user, KeyFile: asString(via["key_file"]), UseAgent: true,
		AskPassword: a.askPassword(host, user), AskPassphrase: a.askPassphrase, HostKey: a.sshHostKeyCallback(host, port),
	})
	if err != nil {
		return nil, fmt.Errorf("SSH gateway %s: %w", host, err)
	}
	return &vncTarget{dial: client.Dial, done: func() { _ = client.Close() }}, nil
}

// VncConnect opens a VNC session for a saved session (its saved password is the VNC password),
// directly or through its SSH gateway, view-only when the session says so.
func (a *App) VncConnect(sessionID int64) (*VncView, error) {
	sv, password, err := a.sessionForFiles(sessionID)
	if err != nil {
		return nil, err
	}
	if sv.Protocol != "vnc" {
		return nil, errors.New("not a VNC session")
	}
	port := sv.Port
	if port == 0 {
		port = 5900
	}
	m, _, err := sessionRawByID(sessionID)
	if err != nil {
		return nil, err
	}
	opts := subMap(m, "vnc")
	target := &vncTarget{}
	if via := subMap(opts, "via_ssh"); asString(via["host"]) != "" {
		if target, err = a.vncGateway(via); err != nil {
			return nil, err
		}
	}
	target.viewOnly = asBool(opts["view_only"])
	// No saved password: ask (with "Save password"), and keep it only once the server accepted it.
	var saver *passwordSaver
	if password == "" {
		saver = &passwordSaver{sessionID: sessionID}
		pw, err := a.ask(fmt.Sprintf("Enter the VNC password for %s:%d", sv.Host, port), "", "", saver)()
		if err != nil {
			return nil, err
		}
		password = pw
	}
	view, err := a.vncDial(sv.Host, port, password, target)
	if err != nil && target.done != nil {
		target.done()
	}
	if err == nil && saver != nil {
		_ = saver.commit() // a failed save only means the next connection asks again
	}
	return view, err
}

// HasVncDemo / ConnectVncDemo auto-open a VNC view to RXT_DEMO_VNC = "password@host:port" (lab/demo).
func (a *App) HasVncDemo() bool { return os.Getenv("RXT_DEMO_VNC") != "" }

func (a *App) ConnectVncDemo() (*VncView, error) {
	v := os.Getenv("RXT_DEMO_VNC")
	if v == "" {
		return nil, errors.New("no demo target")
	}
	password, hostport := "", v
	if at := strings.LastIndexByte(v, '@'); at >= 0 {
		password, hostport = v[:at], v[at+1:]
	}
	host, port := hostport, 5900
	if c := strings.LastIndexByte(hostport, ':'); c >= 0 {
		host = hostport[:c]
		if p, err := strconv.Atoi(hostport[c+1:]); err == nil {
			port = p
		}
	}
	return a.vncDial(host, port, password, &vncTarget{})
}

func (a *App) vncDial(host string, port int, password string, target *vncTarget) (*VncView, error) {
	a.mu.Lock()
	a.seq++
	tabID := "v" + strconv.Itoa(a.seq)
	a.mu.Unlock()

	onFrame := func(r vncconn.Rect) {
		if a.emit != nil {
			a.emit("vnc:frame:"+tabID, map[string]any{
				"x": r.X, "y": r.Y, "w": r.W, "h": r.H,
				"png": base64.StdEncoding.EncodeToString(r.PNG),
			})
		}
	}
	onClose := func(err error) {
		a.mu.Lock()
		delete(a.vnc, tabID)
		delete(a.vncViewOnly, tabID)
		a.mu.Unlock()
		if target.done != nil {
			target.done()
		}
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		if a.emit != nil {
			a.emit("vnc:close:"+tabID, msg)
		}
	}
	var view *VncView
	sess, err := vncconn.Connect(vncconn.Config{Host: host, Port: port, Password: password, Dial: target.dial},
		func(w, h int, name string) { view = &VncView{TabID: tabID, Width: w, Height: h, Name: name} },
		onFrame, onClose)
	if err != nil {
		return nil, err
	}
	sess.OnCutText(func(text string) {
		if a.emit != nil {
			a.emit("vnc:clipboard:"+tabID, text)
		}
	})
	a.mu.Lock()
	if a.vnc == nil {
		a.vnc = map[string]*vncconn.Session{}
	}
	a.vnc[tabID] = sess
	if target.viewOnly {
		if a.vncViewOnly == nil {
			a.vncViewOnly = map[string]bool{}
		}
		a.vncViewOnly[tabID] = true
	}
	a.mu.Unlock()
	if view == nil {
		view = &VncView{TabID: tabID}
	}
	view.ViewOnly = target.viewOnly
	return view, nil
}

// VncSendClipboard sends local clipboard text to the VNC server (ClientCutText).
func (a *App) VncSendClipboard(tabID, text string) error {
	s := a.vncSession(tabID)
	if s == nil {
		return nil
	}
	return s.SendCutText(text)
}

// VncStart begins streaming frames (call after subscribing to vnc:frame events).
func (a *App) VncStart(tabID string) error {
	s := a.vncSession(tabID)
	if s == nil {
		return errors.New("the VNC session has closed")
	}
	return s.Start()
}

// VncPointer sends a mouse event (buttonMask bit0=left,1=middle,2=right,3=wheelUp,4=wheelDown).
func (a *App) VncPointer(tabID string, buttonMask, x, y int) error {
	s := a.vncInput(tabID)
	if s == nil {
		return nil
	}
	return s.Pointer(buttonMask, x, y)
}

// VncKey sends a key event (X keysym).
func (a *App) VncKey(tabID string, down bool, keysym int) error {
	s := a.vncInput(tabID)
	if s == nil {
		return nil
	}
	return s.Key(down, uint32(keysym))
}

// VncDisconnect closes a VNC session.
func (a *App) VncDisconnect(tabID string) error {
	a.mu.Lock()
	s := a.vnc[tabID]
	delete(a.vnc, tabID)
	a.mu.Unlock()
	if s == nil {
		return nil
	}
	return s.Close()
}

// vncInput is the session to send input to: none for a view-only session.
func (a *App) vncInput(tabID string) *vncconn.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.vncViewOnly[tabID] {
		return nil
	}
	return a.vnc[tabID]
}

// VncSetPaused stops framebuffer update requests while the tab is hidden and resumes when shown.
func (a *App) VncSetPaused(tabID string, paused bool) error {
	s := a.vncSession(tabID)
	if s == nil {
		return nil
	}
	return s.SetPaused(paused)
}

func (a *App) vncSession(tabID string) *vncconn.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.vnc[tabID]
}
