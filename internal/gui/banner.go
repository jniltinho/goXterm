package gui

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/user"
	"strings"

	"rustxterm-go/internal/localconn"
)

// Session banner: the info box a terminal tab shows when it connects (parity with the Rust app's
// banner.rs). The backend supplies the facts — only what this app really does, so unsupported
// features show as ✗ — and Terminal.vue draws the box at the tab's width in the scheme's colours.

// BannerLine is one row: "head" (Text + Target), "check" (On, Text, Note) or "muted" (Text).
type BannerLine struct {
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Target string `json:"target,omitempty"`
	On     bool   `json:"on,omitempty"`
	Note   string `json:"note,omitempty"`
}

// Banner is the box: Title in the top border, Hint in the bottom border.
type Banner struct {
	Title string       `json:"title"`
	Hint  string       `json:"hint"`
	Lines []BannerLine `json:"lines"`
}

// Attach is what Terminal.vue receives once it listens to a tab: the banner (nil for none), the
// output that arrived before it was listening (base64), and the close reason if the stream already
// ended.
type Attach struct {
	Banner   *Banner  `json:"banner"`
	Early    string   `json:"early"`
	Closed   bool     `json:"closed"`
	CloseMsg string   `json:"closeMsg"`
	End      *TermEnd `json:"end"` // how it ended, when Closed
}

// earlyTab holds a stream tab's output until the SPA attaches, so nothing is lost between connect
// returning and the frontend subscribing, and the banner lands before the server's first bytes.
type earlyTab struct {
	buf      []byte
	banner   *Banner
	closed   bool
	closeMsg string
	end      *TermEnd
}

// shortcut: output beyond 1 MiB before attach is dropped; the SPA attaches milliseconds after connect.
const earlyCap = 1 << 20

// TermAttach hands a tab's banner and buffered output to the SPA and switches the tab to live events.
// Terminal.vue calls it right after subscribing to term:out/term:close.
func (a *App) TermAttach(tabID string) Attach {
	a.mu.Lock()
	e := a.early[tabID]
	delete(a.early, tabID)
	a.mu.Unlock()
	if e == nil {
		return Attach{}
	}
	return Attach{
		Banner: e.banner, Early: base64.StdEncoding.EncodeToString(e.buf),
		Closed: e.closed, CloseMsg: e.closeMsg, End: e.end,
	}
}

// dropEarly forgets a tab whose connect failed (it never reaches the SPA).
func (a *App) dropEarly(tabID string) {
	a.mu.Lock()
	delete(a.early, tabID)
	a.mu.Unlock()
}

// setBanner attaches the banner to a tab that has not been attached yet.
func (a *App) setBanner(tabID string, b *Banner) {
	a.mu.Lock()
	if e := a.early[tabID]; e != nil {
		e.banner = b
	}
	a.mu.Unlock()
}

// bannerHint is the bottom border, as in the Rust app (both are implemented in Terminal.vue).
const bannerHint = "Ctrl+wheel, Ctrl+=/-/0: zoom • Ctrl+Shift+F: find"

func (a *App) bannerTitle() string { return "goXterm " + a.version }

func check(on bool, text, note string) BannerLine {
	return BannerLine{Kind: "check", On: on, Text: text, Note: note}
}

func hostPort(host string, port, def int) string {
	if port == 0 || port == def {
		return host
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// bannerFwd is the connection path and forwarding of an SSH tab for its banner.
type bannerFwd struct {
	gateway    string // jump hosts, "a → b"; "" = direct
	x11, agent bool
}

// sshBanner describes an SSH tab like the Rust app: target, the features this client actually runs
// (✗ where not implemented yet), and the server id + host key.
// fwd is what the session forwards (gateway hop names, X11, agent).
func (a *App) sshBanner(userName, host string, port, keepalive int, serverVersion, keyAlgo, keyFP string, fwd bannerFwd) *Banner {
	target := hostPort(host, port, 22)
	if userName != "" {
		target = userName + "@" + target
	}
	via := check(true, "Direct connection", "")
	if fwd.gateway != "" {
		via = check(true, "SSH gateway", fwd.gateway)
	}
	keepNote := ""
	if keepalive > 0 {
		keepNote = fmt.Sprintf("every %d s", keepalive)
	}
	lines := []BannerLine{
		{Kind: "head", Text: "SSH session to ", Target: target},
		via,
		check(false, "Compression", ""), // golang.org/x/crypto/ssh has no zlib compression
		check(true, "SFTP browser", ""),
		check(fwd.x11, "X11 forwarding", ""),
		check(fwd.agent, "Agent forwarding", ""),
		check(keepalive > 0, "Keepalive", keepNote),
		check(true, "Follow terminal folder", ""), // the SFTP side panel follows OSC 7 / the shell's cwd
	}
	server := strings.TrimSpace(serverVersion)
	if keyFP != "" {
		fp := keyFP // "SHA256:…", first 23 chars as in the Rust app
		if r := []rune(fp); len(r) > 23 {
			fp = string(r[:23]) + "…"
		}
		if server != "" {
			server += ", "
		}
		server += keyAlgo + " " + fp
	}
	if server != "" {
		lines = append(lines, BannerLine{Kind: "muted", Text: "Server: " + server})
	}
	return &Banner{Title: a.bannerTitle(), Hint: bannerHint, Lines: lines}
}

// telnetBanner warns that Telnet is clear text.
func (a *App) telnetBanner(host string, port int) *Banner {
	return &Banner{Title: a.bannerTitle(), Hint: bannerHint, Lines: []BannerLine{
		{Kind: "head", Text: "Telnet session to ", Target: hostPort(host, port, 23)},
		check(false, "Encryption", "traffic and passwords are sent in clear text"),
	}}
}

// localBanner is the welcome box of a local terminal: who/where, the shell and folder, and tips.
func (a *App) localBanner(shell string) *Banner {
	who := "user"
	if u, err := user.Current(); err == nil {
		who = u.Username
	}
	host, _ := os.Hostname()
	cwd := localconn.StartDir()
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return &Banner{Title: a.bannerTitle(), Hint: bannerHint, Lines: []BannerLine{
		{Kind: "head", Text: "Local terminal on ", Target: who + "@" + host},
		{Kind: "muted", Text: "Shell:  " + shell},
		{Kind: "muted", Text: "Folder: " + cwd},
		{Kind: "muted", Text: "• Ctrl+Shift+N: new session"},
		{Kind: "muted", Text: "• Ctrl+Shift+W: close this tab"},
	}}
}
