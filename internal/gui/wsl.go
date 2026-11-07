package gui

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"rustxterm-go/internal/localconn"
	"rustxterm-go/internal/wslconn"
)

// WslAvailable reports whether WSL sessions can run here (Windows with a WSL service).
func (a *App) WslAvailable() bool { return wslconn.Available() }

// WslDistributions lists the installed distributions (default first) for the session editor.
func (a *App) WslDistributions() ([]wslconn.Distribution, error) { return wslconn.Distributions() }

// WslConnect opens a saved WSL session: wsl.exe on a ConPTY with the session's distribution, user
// and start folder; its startup commands are typed once the shell first prints (Rust parity).
func (a *App) WslConnect(sessionID int64) (string, error) {
	if !wslconn.Available() {
		return "", wslconn.ErrUnavailable
	}
	st, err := openStore()
	if err != nil {
		return "", err
	}
	rows, err := st.Sessions()
	st.Close()
	if err != nil {
		return "", err
	}
	var opts wslconn.Options
	found := false
	for _, m := range rows {
		if asInt64(m["id"]) == sessionID {
			if b, err := json.Marshal(subMap(m, "wsl")); err == nil {
				_ = json.Unmarshal(b, &opts)
			}
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("session %d not found", sessionID)
	}
	return a.wslOpen(opts)
}

func (a *App) wslOpen(opts wslconn.Options) (string, error) {
	args, startup, err := wslconn.LaunchArgs(opts, wslconn.SupportsCD())
	if err != nil {
		return "", err
	}
	tabID, onData, onClose := a.newStreamTab()
	var once sync.Once
	data := func(b []byte) {
		onData(b)
		if len(startup) > 0 {
			once.Do(func() { go a.typeLines(tabID, startup) })
		}
	}
	sess, err := localconn.StartCommand(wslconn.Exe(), args, wslconn.WorkDir(), 80, 24, data, onClose)
	if err != nil {
		a.dropEarly(tabID)
		return "", err
	}
	a.setBanner(tabID, a.wslBanner(opts))
	a.mu.Lock()
	a.term[tabID] = sess
	a.mu.Unlock()
	return tabID, nil
}

// typeLines sends the startup commands, each with Return, after the shell's first prompt settles.
func (a *App) typeLines(tabID string, lines []string) {
	time.Sleep(400 * time.Millisecond)
	for _, l := range lines {
		if s := a.session(tabID); s != nil {
			_ = s.Write([]byte(l + "\r"))
		}
	}
}

// wslBanner is the Rust banner for WSL: the distribution, then user and folder when set.
func (a *App) wslBanner(o wslconn.Options) *Banner {
	lines := []BannerLine{{Kind: "head", Text: "WSL: ", Target: o.Label()}}
	if o.User != "" {
		lines = append(lines, BannerLine{Kind: "muted", Text: "User:   " + o.User})
	}
	if o.StartFolder != "" {
		lines = append(lines, BannerLine{Kind: "muted", Text: "Folder: " + o.StartFolder})
	}
	lines = append(lines,
		BannerLine{Kind: "muted", Text: "• Ctrl+Shift+N: new session"},
		BannerLine{Kind: "muted", Text: "• Ctrl+Shift+W: close this tab"})
	return &Banner{Title: a.bannerTitle(), Hint: bannerHint, Lines: lines}
}
