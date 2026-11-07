package gui

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"

	"rustxterm-go/internal/control"
	"rustxterm-go/internal/rdpconn"
	"rustxterm-go/internal/store"
	"rustxterm-go/internal/vncconn"
)

// App is the service the SPA calls (Wails binds it as "rustxterm-go/internal/gui.App.<Method>").
// It lists sessions/folders from the Rust store and runs SSH terminals, streaming output to the SPA
// as Wails events (migrate-gui-to-go-wails phases 4–5).
type App struct {
	emit     func(name string, data any)        // set by gui.Run; emits Wails events to the SPA
	pickFile func() (string, error)             // set by gui.Run; native open-file dialog (SSH key picker)
	quit     func()                             // set by gui.Run; quits the app (Terminal → Exit menu)
	savePath func(name string) (string, error)  // set by gui.Run; native save-file dialog
	openPath func(title string) (string, error) // set by gui.Run; native open-file dialog
	clip     clipboard                          // set by gui.Run; the system clipboard
	version  string                             // set by gui.Run, shown in Settings → Diagnostics

	mu     sync.Mutex
	seq    int
	term   map[string]termStream       // tabId → live terminal (SSH, Telnet or Serial)
	remote map[string]remoteFS         // tabId → live SFTP/FTP file session
	vnc    map[string]*vncconn.Session // tabId → live VNC session
	rdp    map[string]*rdpconn.Session // tabId → live RDP session
	trust  map[string]chan string      // reqId → pending SSH host-key trust decision
	pass   map[string]chan passAns     // reqId → pending interactive password entry
	mon    map[string]chan struct{}    // tabId → stop channel for its remote-monitoring loop
	tun    map[string]*tunRun          // via+name → running port-forwarding tunnel
	early  map[string]*earlyTab        // tabId → output/banner held until the SPA attaches
	// shellPID caches each SSH tab's login-shell PID for "Follow terminal folder" (-1 unsupported).
	shellPID map[string]int
	multi    multiExec         // MultiExec: broadcast typed input to the selected terminals
	ctl      *control.Listener // agent control socket listener (nil when agent control is off)
}

// termStream is any byte-stream terminal the SPA drives the same way (SSH/Telnet/Serial); each
// backend's Session satisfies it, so Send/Resize/Disconnect and the term:out/term:close events are
// shared (migrate-gui-to-go-wails — core separation: one UI contract, one client per protocol).
type termStream interface {
	Write(p []byte) error
	Resize(cols, rows int) error
	Close() error
}

// SessionView is one row for the sidebar; credentials are never included (only saved/absent).
type SessionView struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Folder   *int64 `json:"folder"`
	Icon     string `json:"icon"`
	Password string `json:"password"`
}

// FolderView is one session-tree folder.
type FolderView struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Parent *int64 `json:"parent"`
	Icon   string `json:"icon"`
}

// Sessions returns every saved session, sorted as the store returns them (by name).
func (a *App) Sessions() ([]SessionView, error) {
	st, err := openStore()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	rows, err := st.Sessions()
	if err != nil {
		return nil, err
	}
	out := make([]SessionView, 0, len(rows))
	for _, m := range rows {
		out = append(out, SessionView{
			ID:       asInt64(m["id"]),
			Name:     asString(m["name"]),
			Protocol: asString(m["protocol"]),
			Host:     asString(m["host"]),
			Port:     int(asInt64(m["port"])),
			User:     asString(m["user"]),
			Folder:   asOptInt64(m["folder"]),
			Icon:     asString(m["icon"]),
			Password: asString(m["password"]),
		})
	}
	return out, nil
}

// Folders returns every folder.
func (a *App) Folders() ([]FolderView, error) {
	st, err := openStore()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	rows, err := st.Folders()
	if err != nil {
		return nil, err
	}
	out := make([]FolderView, 0, len(rows))
	for _, m := range rows {
		out = append(out, FolderView{
			ID:     asInt64(m["id"]),
			Name:   asString(m["name"]),
			Parent: asOptInt64(m["parent"]),
			Icon:   asString(m["icon"]),
		})
	}
	return out, nil
}

// masterPW caches the master password after an in-UI unlock (in memory only, never logged/persisted).
var masterPW struct {
	sync.Mutex
	pw string
}

// openStore unlocks via the keyring wrap, else the password cached by Unlock, else RXT_MASTER_PASSWORD
// (development/testing). A locked store returns this error, which the SPA shows as the unlock prompt.
func openStore() (*store.Store, error) {
	dir := store.DataDir()
	if st, ok, _ := store.OpenWithKeyring(dir, store.SystemKeyring()); ok {
		return st, nil
	}
	masterPW.Lock()
	pw := masterPW.pw
	masterPW.Unlock()
	if pw == "" {
		pw = os.Getenv("RXT_MASTER_PASSWORD")
	}
	if pw != "" {
		return store.Open(dir, pw)
	}
	return nil, errors.New("store is locked: unlock with the keyring or set RXT_MASTER_PASSWORD")
}

// Unlock opens the store with the master password and, on success, caches it in memory for the rest
// of the session. The password is never logged or written anywhere.
func (a *App) Unlock(password string) error {
	st, err := store.Open(store.DataDir(), password)
	if err != nil {
		return err
	}
	st.Close()
	masterPW.Lock()
	masterPW.pw = password
	masterPW.Unlock()
	return nil
}

// Locked reports whether the store still needs a master password (no keyring, no cached, no env).
func (a *App) Locked() bool {
	st, err := openStore()
	if err != nil {
		return true
	}
	st.Close()
	return false
}

// sessionByID loads one saved session as a SessionView (no credentials), shared by the per-protocol
// Connect methods.
func sessionByID(id int64) (SessionView, error) {
	st, err := openStore()
	if err != nil {
		return SessionView{}, err
	}
	defer st.Close()
	rows, err := st.Sessions()
	if err != nil {
		return SessionView{}, err
	}
	for _, m := range rows {
		if asInt64(m["id"]) == id {
			return viewOf(m), nil
		}
	}
	return SessionView{}, fmt.Errorf("session %d not found", id)
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

// sessionRawByID returns the stored session JSON (with its nested protocol options, e.g. m["rdp"],
// m["serial"]) and the saved password, for connect paths that read per-protocol settings.
func sessionRawByID(id int64) (map[string]any, string, error) {
	st, err := openStore()
	if err != nil {
		return nil, "", err
	}
	defer st.Close()
	rows, err := st.Sessions()
	if err != nil {
		return nil, "", err
	}
	for _, m := range rows {
		if asInt64(m["id"]) == id {
			password := ""
			if secret, ok, _ := st.Credential(id, store.CredPassword); ok {
				password = string(secret)
			}
			return m, password, nil
		}
	}
	return nil, "", fmt.Errorf("session %d not found", id)
}

func subMap(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}

func asBool(v any) bool { b, _ := v.(bool); return b }

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case int:
		return int64(n)
	}
	return 0
}

func asOptInt64(v any) *int64 {
	if v == nil {
		return nil
	}
	n := asInt64(v)
	return &n
}

// clipboard is the system clipboard (Wails' native one, so no WebView permission prompt).
type clipboard interface {
	Text() (string, bool)
	SetText(string) bool
}
