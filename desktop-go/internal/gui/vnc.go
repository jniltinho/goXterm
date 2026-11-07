package gui

import (
	"encoding/base64"
	"errors"
	"os"
	"strconv"
	"strings"

	"rustxterm-go/internal/vncconn"
)

// VncView is returned on connect: the tab id and desktop geometry for sizing the canvas.
type VncView struct {
	TabID  string `json:"tabId"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Name   string `json:"name"`
}

// VncConnect opens a VNC session for a saved session (its saved password is the VNC password).
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
	return a.vncDial(sv.Host, port, password)
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
	return a.vncDial(host, port, password)
}

func (a *App) vncDial(host string, port int, password string) (*VncView, error) {
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
		a.mu.Unlock()
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		if a.emit != nil {
			a.emit("vnc:close:"+tabID, msg)
		}
	}
	var view *VncView
	sess, err := vncconn.Connect(vncconn.Config{Host: host, Port: port, Password: password},
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
	a.mu.Unlock()
	if view == nil {
		view = &VncView{TabID: tabID}
	}
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
	s := a.vncSession(tabID)
	if s == nil {
		return nil
	}
	return s.Pointer(buttonMask, x, y)
}

// VncKey sends a key event (X keysym).
func (a *App) VncKey(tabID string, down bool, keysym int) error {
	s := a.vncSession(tabID)
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

func (a *App) vncSession(tabID string) *vncconn.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.vnc[tabID]
}
