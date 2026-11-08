package gui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"rustxterm-go/internal/control"
	"rustxterm-go/internal/serialconn"
	"rustxterm-go/internal/store"
)

// Agent control (Rust engine/src/control.rs, migrate-gui-to-go-wails task 3.3): the control-channel
// server, the authorization boundary and the live operations. Rules for every request:
//   - only cli-live / mcp-live requests are accepted here; the fixed matrix (control.Allowed) and
//     GUI-only ops are checked first;
//   - store reads (lists, show, export) carry no session content and need no policy;
//   - ops on a session (open, tab ops, tunnels) check its agent policy (control.Authorize): deny
//     rejects, ask asks the GUI for any write effect, allow runs; content reads also need the
//     content-read flag. Local and WSL terminals are never available to agents;
//   - every request is audited (who, entry, op, target, decision, result — never input data or
//     credentials).

const (
	approvalTTL      = 60 * time.Second // unanswered approvals are denied
	maxWait          = 600 * time.Second
	defaultReadLines = 100
	defaultBreakMs   = 250
	maxBreakMs       = 5000
	localDenied      = "local terminals are not available to agents"
)

// agentTab is the saved session a tab was opened from (its policy applies to agent ops on the tab).
// Session 0 (quick connect, demo) has no policy row and so is denied.
type agentTab struct {
	Session  int64
	Name     string
	Protocol string
}

// noteTab records the saved session behind a tab.
func (a *App) noteTab(tabID string, session int64, name, protocol string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tabInfo == nil {
		a.tabInfo = map[string]agentTab{}
	}
	a.tabInfo[tabID] = agentTab{session, name, protocol}
}

// ctlError is an agent-facing error with its control code; the message never carries secrets.
type ctlError struct{ code, msg string }

func (e *ctlError) Error() string { return e.msg }

func usageErr(format string, args ...any) error {
	return &ctlError{control.CodeUsage, fmt.Sprintf(format, args...)}
}
func deniedErr(msg string) error { return &ctlError{control.CodeDenied, msg} }
func failedErr(msg string) error { return &ctlError{control.CodeFailed, msg} }

// startControl starts the agent control listener when the agent_control setting is on. Each
// connection is served on its own goroutine; failures are logged, not fatal.
func (a *App) startControl(log *slog.Logger) {
	s, err := store.LoadSettings()
	if err != nil || !s.AgentControl {
		return // off by default
	}
	dir, err := control.RuntimeDir()
	if err != nil {
		log.Warn("agent control off: " + err.Error())
		return
	}
	ln, err := control.Listen(dir)
	if err != nil {
		log.Warn("agent control not started: " + err.Error())
		return
	}
	a.ctl = ln
	log.Info("agent control listening")
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go a.serveControlConn(c, log)
		}
	}()
}

// stopControl stops the listener (removing the socket file).
func (a *App) stopControl() {
	if a.ctl != nil {
		_ = a.ctl.Close()
		a.ctl = nil
	}
}

// serveControlConn answers requests until the client closes; a broken or oversized frame closes it.
func (a *App) serveControlConn(c control.Conn, log *slog.Logger) {
	defer c.Close()
	for {
		var req control.Request
		ok, err := control.ReadMessage(c, &req)
		if !ok || err != nil {
			return
		}
		if control.WriteMessage(c, a.dispatch(req, log)) != nil {
			return
		}
	}
}

// dispatch runs one request through the boundary and audits it.
func (a *App) dispatch(req control.Request, log *slog.Logger) control.Response {
	result, decision, err := a.agentRequest(req)
	outcome := "success"
	if err != nil {
		outcome = err.Error()
	}
	// shortcut: the audit trail goes to the app log, not the store's audit table — the Go store
	// re-encrypts the whole file on every write, so a write per agent request could race GUI edits
	// and lose them; move it to the table once the store writes in place.
	log.Info("agent control", "client", trunc(req.Client, 128), "entry", req.Entry, "op", req.Op,
		"target", describe(req.AgentOp), "decision", decision, "result", outcome)
	if err != nil {
		code := control.CodeFailed
		var ce *ctlError
		if errors.As(err, &ce) {
			code = ce.code
		}
		return control.Err(req.ID, code, err.Error())
	}
	return control.OK(req.ID, result)
}

// agentRequest is the authorization boundary; it returns the result, the audit decision and the error.
func (a *App) agentRequest(req control.Request) (any, string, error) {
	op := req.AgentOp
	if req.Entry != control.EntryCliLive && req.Entry != control.EntryMcpLive {
		return nil, "denied", deniedErr(req.Entry + " requests are not accepted on the control channel")
	}
	// Matrix and GUI-only rejections need no store lookup.
	if d, msg := control.Authorize(req.Entry, op.Op, &control.Policy{Policy: "allow", ContentRead: true}); d == control.Deny {
		return nil, "denied", deniedErr(msg)
	}
	if control.Group(op.Op) == control.GroupRead {
		v, err := a.storeRead(op)
		return v, "allowed", err
	}
	ctx, err := a.resolve(op)
	if err != nil {
		return nil, "denied", err
	}
	decision := "allowed"
	switch d, msg := control.Authorize(req.Entry, op.Op, ctx.policy); d {
	case control.Deny:
		return nil, "denied", deniedErr(msg)
	case control.NeedsApproval:
		if !a.approve(req.Client, op, ctx) {
			return nil, "denied", deniedErr("not approved (denied, expired or parameters changed)")
		}
		decision = "approved"
	}
	v, err := a.execute(op, ctx)
	var ce *ctlError
	if errors.As(err, &ce) && ce.code == control.CodeDenied {
		decision = "denied"
	}
	return v, decision, err
}

// storeRead serves the Read group from the store; a store that cannot be opened is locked.
func (a *App) storeRead(op control.AgentOp) (any, error) {
	st, err := openStore()
	if err != nil {
		return nil, &ctlError{control.CodeLocked, err.Error()}
	}
	defer st.Close()
	switch op.Op {
	case "list_sessions":
		return st.Sessions()
	case "list_folders":
		return st.Folders()
	case "list_tunnels":
		return st.Tunnels()
	case "export":
		return st.Export()
	case "show_session":
		list, err := st.Sessions()
		if err != nil {
			return nil, err
		}
		s, err := findSession(list, op.Session)
		if err != nil {
			return nil, err
		}
		p, err := st.Policy(asInt64(s["id"]))
		if err != nil {
			return nil, err
		}
		s["agent_policy"], s["content_read"] = p.Policy, p.ContentRead
		return s, nil
	}
	return nil, failedErr("unhandled read op " + op.Op)
}

// findSession matches a session by id or exact name; an ambiguous name is refused.
func findSession(list []map[string]any, ref *control.SessionRef) (map[string]any, error) {
	if ref == nil || (ref.ID == nil && ref.Name == nil) {
		return nil, usageErr("missing session")
	}
	var found []map[string]any
	for _, s := range list {
		if (ref.ID != nil && asInt64(s["id"]) == *ref.ID) || (ref.Name != nil && asString(s["name"]) == *ref.Name) {
			found = append(found, s)
		}
	}
	switch {
	case len(found) == 1:
		return found[0], nil
	case len(found) > 1:
		return nil, usageErr("several sessions are named %q; use the id", *ref.Name)
	case ref.ID != nil:
		return nil, usageErr("no session %d", *ref.ID)
	}
	return nil, usageErr("no session named %q", *ref.Name)
}

// agentCtx is what a live request acts on, resolved before authorization.
type agentCtx struct {
	tab     string // backend tab id ("t3", "v5", …)
	session agentTab
	policy  *control.Policy // nil for ops that act on no session
	tunnel  map[string]any
}

func isLocal(protocol string) bool { return protocol == "local" || protocol == "wsl" }

func (a *App) resolve(op control.AgentOp) (agentCtx, error) {
	if op.Tab != nil {
		tab, info, ok := a.tabByNum(*op.Tab)
		if !ok {
			return agentCtx{}, usageErr("no tab %d", *op.Tab)
		}
		if isLocal(info.Protocol) {
			return agentCtx{}, deniedErr(localDenied)
		}
		pol, err := policyOf(info.Session)
		return agentCtx{tab: tab, session: info, policy: pol}, err
	}
	if op.Op != "open" && op.Op != "tunnel_start" && op.Op != "tunnel_stop" {
		if control.TargetsSession(op.Op) {
			return agentCtx{}, usageErr("missing tab")
		}
		return agentCtx{}, nil
	}
	st, err := openStore()
	if err != nil {
		return agentCtx{}, &ctlError{control.CodeLocked, err.Error()}
	}
	defer st.Close()
	list, err := st.Sessions()
	if err != nil {
		return agentCtx{}, err
	}
	var ctx agentCtx
	ref := op.Session
	if op.Op != "open" {
		tunnels, err := st.Tunnels()
		if err != nil {
			return agentCtx{}, err
		}
		for _, t := range tunnels {
			if asString(t["name"]) == op.Name {
				ctx.tunnel = t
				break
			}
		}
		if ctx.tunnel == nil {
			return agentCtx{}, usageErr("no tunnel named %q", op.Name)
		}
		via := asInt64(ctx.tunnel["via"])
		ref = &control.SessionRef{ID: &via}
	}
	s, err := findSession(list, ref)
	if err != nil {
		return agentCtx{}, err
	}
	ctx.session = agentTab{asInt64(s["id"]), asString(s["name"]), asString(s["protocol"])}
	if isLocal(ctx.session.Protocol) {
		return agentCtx{}, deniedErr(localDenied)
	}
	p, err := st.Policy(ctx.session.Session)
	if err != nil {
		return agentCtx{}, err
	}
	ctx.policy = &control.Policy{Policy: p.Policy, ContentRead: p.ContentRead}
	return ctx, nil
}

// policyOf reads a session's policy; session 0 (no saved session) is deny.
func policyOf(session int64) (*control.Policy, error) {
	if session == 0 {
		return &control.Policy{Policy: "deny"}, nil
	}
	st, err := openStore()
	if err != nil {
		return nil, &ctlError{control.CodeLocked, err.Error()}
	}
	defer st.Close()
	p, err := st.Policy(session)
	if err != nil {
		return nil, err
	}
	return &control.Policy{Policy: p.Policy, ContentRead: p.ContentRead}, nil
}

// tabKinds maps a backend tab id prefix to the kind shown when the tab has no saved session.
var tabKinds = []struct{ prefix, kind string }{{"t", "terminal"}, {"v", "vnc"}, {"r", "rdp"}, {"s", "sftp"}, {"f", "ftp"}}

// liveTab reports whether a backend tab is open; the caller holds a.mu. A "t" id in a.remote is the
// side SFTP browser of a terminal, not a tab of its own.
func (a *App) liveTab(id string) bool {
	switch id[0] {
	case 't':
		return a.term[id] != nil
	case 'v':
		return a.vnc[id] != nil
	case 'r':
		return a.rdp[id] != nil
	}
	return a.remote[id] != nil
}

// tabByNum finds the open tab with the agent-facing number n (the backend id without its prefix).
func (a *App) tabByNum(n uint64) (string, agentTab, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, k := range tabKinds {
		id := k.prefix + strconv.FormatUint(n, 10)
		if a.liveTab(id) {
			info := a.tabInfo[id]
			if info.Protocol == "" {
				info.Protocol = k.kind
			}
			return id, info, true
		}
	}
	return "", agentTab{}, false
}

func tabNum(id string) uint64 { n, _ := strconv.ParseUint(id[1:], 10, 64); return n }

// approve asks the GUI and waits for the answer; an unanswered request is denied after approvalTTL.
// The approval id is single-use and answers only this request, whose parameters cannot change while
// it waits (the Rust parameter hash binds the same thing across processes).
func (a *App) approve(client string, op control.AgentOp, ctx agentCtx) bool {
	if a.emit == nil {
		return false // no window to ask in
	}
	a.mu.Lock()
	a.apprSeq++
	id := a.apprSeq
	ch := make(chan bool, 1)
	if a.approvals == nil {
		a.approvals = map[uint64]chan bool{}
	}
	a.approvals[id] = ch
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.approvals, id)
		a.mu.Unlock()
		a.emit("agent:approval:done", id)
	}()
	a.emit("agent:approval", map[string]any{
		"id": id, "client": trunc(client, 128), "command": op.Op, "session": ctx.session.Name,
		"target": describe(op), "expiresAt": time.Now().Add(approvalTTL).Unix(),
	})
	select {
	case ok := <-ch:
		return ok
	case <-time.After(approvalTTL):
		return false
	}
}

// AgentApprove is the GUI's answer to an "agent:approval" prompt.
func (a *App) AgentApprove(id uint64, approve bool) {
	a.mu.Lock()
	ch := a.approvals[id]
	delete(a.approvals, id)
	a.mu.Unlock()
	if ch != nil {
		ch <- approve
	}
}

func (a *App) execute(op control.AgentOp, ctx agentCtx) (any, error) {
	switch op.Op {
	case "open":
		return a.agentOpen(ctx.session)
	case "list_tabs":
		return a.listTabs(), nil
	case "send":
		return a.agentSend(ctx.tab, op.Data)
	case "read":
		n := defaultReadLines
		if op.Lines != nil {
			n = min(int(*op.Lines), tailLines)
		}
		text, _ := a.tailText(ctx.tab, func(t *tail) string { return t.last(n) })
		return map[string]any{"text": text}, nil
	case "wait":
		timeout := 10 * time.Second
		if op.TimeoutMs != nil {
			timeout = time.Duration(min(*op.TimeoutMs, uint64(maxWait/time.Millisecond))) * time.Millisecond
		}
		return a.agentWait(ctx.tab, op.Pattern, timeout, ctx.policy.ContentRead)
	case "screenshot":
		return a.screenshot(ctx.tab)
	case "close":
		a.agentClose(ctx.tab)
		return map[string]any{"closed": tabNum(ctx.tab)}, nil
	case "tunnel_start":
		if err := a.startTunnel(ctx.tunnel); err != nil {
			return nil, failedErr(err.Error())
		}
		return map[string]any{"tunnel": op.Name, "state": "running"}, nil
	case "tunnel_stop":
		via := asInt64(ctx.tunnel["via"])
		a.mu.Lock()
		running := a.tun[tunKey(via, op.Name)] != nil
		a.mu.Unlock()
		_ = a.TunnelStop(via, op.Name)
		return map[string]any{"tunnel": op.Name, "stopped": running}, nil
	case "files_ls":
		return a.filesLs(ctx.tab, op.Path)
	case "files_get":
		return a.filesGet(ctx.tab, op.Remote, op.Local)
	case "files_put":
		return a.filesPut(ctx.tab, op.Local, op.Remote)
	case "serial_send_break":
		s, err := a.serialTab(ctx.tab)
		if err != nil {
			return nil, err
		}
		ms := uint64(defaultBreakMs)
		if op.Ms != nil {
			ms = min(*op.Ms, maxBreakMs)
		}
		if err := s.SendBreak(ms); err != nil {
			return nil, failedErr(err.Error())
		}
		return map[string]any{"tab": tabNum(ctx.tab), "break_ms": ms}, nil
	case "serial_set_lines":
		s, err := a.serialTab(ctx.tab)
		if err != nil {
			return nil, err
		}
		if err := s.SetLines(op.Dtr, op.Rts); err != nil {
			return nil, failedErr(err.Error())
		}
		return map[string]any{"tab": tabNum(ctx.tab), "dtr": op.Dtr, "rts": op.Rts}, nil
	case "serial_list_ports":
		ports, err := serialconn.Ports()
		if err != nil {
			return nil, failedErr(err.Error())
		}
		return ports, nil
	}
	return nil, usageErr("%s is not a live operation", op.Op)
}

// agentOpen opens a saved session in a new tab and tells the SPA to show it.
// shortcut: agents open terminal sessions only (SSH, Telnet, Serial); RDP/VNC/SFTP/FTP tabs are sized
// and started by their SPA view — add them when an agent needs a desktop or a file tab of its own.
func (a *App) agentOpen(s agentTab) (any, error) {
	var tab string
	var err error
	switch s.Protocol {
	case "ssh":
		tab, err = a.Connect(s.Session)
	case "telnet":
		tab, err = a.TelnetConnect(s.Session)
	case "serial":
		tab, err = a.SerialConnect(s.Session)
	default:
		return nil, failedErr(s.Protocol + " sessions cannot be opened by agents yet; open it in the window")
	}
	if err != nil {
		return nil, failedErr(err.Error())
	}
	if a.emit != nil {
		a.emit("agent:open", map[string]any{"tabId": tab, "session": s.Session, "name": s.Name, "protocol": s.Protocol})
	}
	return map[string]any{"tab": tabNum(tab)}, nil
}

func (a *App) listTabs() []map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	ids := map[string]bool{}
	for id := range a.term {
		ids[id] = true
	}
	for id := range a.vnc {
		ids[id] = true
	}
	for id := range a.rdp {
		ids[id] = true
	}
	for id := range a.remote {
		ids[id] = a.liveTab(id)
	}
	out := []map[string]any{}
	for id, live := range ids {
		if !live {
			continue
		}
		info := a.tabInfo[id]
		if info.Protocol == "" {
			for _, k := range tabKinds {
				if strings.HasPrefix(id, k.prefix) {
					info.Protocol = k.kind
				}
			}
		}
		out = append(out, map[string]any{"id": tabNum(id), "name": info.Name, "protocol": info.Protocol, "state": "connected"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["id"].(uint64) < out[j]["id"].(uint64) })
	return out
}

// agentSend sends input to tab and, with MultiExec on, to its group peers whose own policy is allow
// (local terminals are skipped and reported).
func (a *App) agentSend(tab, data string) (any, error) {
	a.mu.Lock()
	var peers []string
	if a.multi.enabled && a.multi.selected[tab] {
		for _, id := range a.multi.targets(tab) {
			if id != tab {
				peers = append(peers, id)
			}
		}
	}
	info := map[string]agentTab{}
	for _, id := range peers {
		info[id] = a.tabInfo[id]
	}
	a.mu.Unlock()
	targets := []string{tab}
	skipped := []uint64{}
	for _, id := range peers {
		if isLocal(info[id].Protocol) {
			skipped = append(skipped, tabNum(id))
		} else if p, err := policyOf(info[id].Session); err == nil && p.Policy == "allow" {
			targets = append(targets, id)
		}
	}
	sent := []uint64{}
	for _, id := range targets {
		a.mu.Lock()
		s := a.term[id]
		if t := a.tails[id]; t != nil {
			t.setMark()
		}
		a.mu.Unlock()
		if s != nil && s.Write([]byte(data)) == nil {
			sent = append(sent, tabNum(id))
		}
	}
	if len(sent) == 0 || sent[0] != tabNum(tab) {
		return nil, failedErr("tab is not accepting input")
	}
	return map[string]any{"sent_to": sent, "skipped": skipped}, nil
}

// tailText runs f on a terminal tab's text tail; false when the tab has none.
func (a *App) tailText(tab string, f func(*tail) string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.tails[tab]
	if t == nil {
		return "", false
	}
	return f(t), true
}

// agentWait polls the output since the last agent send for a substring, or a regexp with "re:".
// The matched text is session content, returned only with the content-read flag.
func (a *App) agentWait(tab, pattern string, timeout time.Duration, content bool) (any, error) {
	var re *regexp.Regexp
	if p, ok := strings.CutPrefix(pattern, "re:"); ok {
		var err error
		if re, err = regexp.Compile(p); err != nil {
			return nil, usageErr("pattern: %v", err)
		}
	}
	deadline := time.Now().Add(timeout)
	for {
		text, ok := a.tailText(tab, (*tail).sinceMark)
		if !ok {
			return nil, usageErr("no tab %d", tabNum(tab))
		}
		if (re != nil && re.MatchString(text)) || (re == nil && strings.Contains(text, pattern)) {
			if !content {
				text = ""
			}
			return map[string]any{"matched": true, "text": text}, nil
		}
		if time.Now().After(deadline) {
			return nil, failedErr("timed out waiting for the pattern")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// screenshot is a PNG of a VNC desktop (base64), or the visible text tail of a terminal tab.
func (a *App) screenshot(tab string) (any, error) {
	switch tab[0] {
	case 'v':
		s := a.vncSession(tab)
		if s == nil {
			return nil, usageErr("no tab %d", tabNum(tab))
		}
		r, ok := s.Snapshot()
		if !ok {
			return nil, failedErr("no desktop image yet")
		}
		return map[string]any{"format": "png", "width": r.W, "height": r.H, "png": base64.StdEncoding.EncodeToString(r.PNG)}, nil
	case 'r':
		// shortcut: the Go RDP client keeps no framebuffer (frames go straight to the SPA); keep one
		// in rdpconn when agents need RDP screenshots.
		return nil, failedErr("no desktop image yet")
	}
	text, _ := a.tailText(tab, func(t *tail) string { return t.last(50) })
	return map[string]any{"format": "text", "text": text}, nil
}

func (a *App) agentClose(tab string) {
	switch tab[0] {
	case 't':
		_ = a.Disconnect(tab)
		a.mu.Lock() // gone for agents now, not when the stream's onClose runs
		delete(a.term, tab)
		delete(a.tails, tab)
		delete(a.tabInfo, tab)
		a.mu.Unlock()
	case 'v':
		_ = a.VncDisconnect(tab)
	case 'r':
		_ = a.RdpDisconnect(tab)
	default:
		_ = a.RemoteDisconnect(tab)
	}
	if a.emit != nil {
		a.emit("agent:close", map[string]any{"tabId": tab})
	}
}

func (a *App) serialTab(tab string) (*serialconn.Session, error) {
	s, ok := a.session(tab).(*serialconn.Session)
	if !ok {
		return nil, usageErr("tab %d is not a serial tab", tabNum(tab))
	}
	return s, nil
}

// agentFS is the file session of a tab: an SFTP/FTP tab, or SFTP over an SSH terminal's connection.
func (a *App) agentFS(tab string) (remoteFS, error) {
	if c := a.remoteClient(tab); c != nil {
		return c, nil
	}
	if tab[0] == 't' {
		if _, err := a.TermFiles(tab); err != nil {
			return nil, failedErr(err.Error())
		}
		if c := a.remoteClient(tab); c != nil {
			return c, nil
		}
	}
	return nil, usageErr("tab %d has no file access", tabNum(tab))
}

func (a *App) filesLs(tab, path string) (any, error) {
	fs, err := a.agentFS(tab)
	if err != nil {
		return nil, err
	}
	if path == "" || path == "~" {
		if path, err = fs.Home(); err != nil {
			return nil, failedErr(err.Error())
		}
	}
	entries, err := fs.List(path)
	if err != nil {
		return nil, failedErr(err.Error())
	}
	return map[string]any{"path": path, "entries": entries}, nil
}

// agentWorkspace opens the agent workspace (Settings agent_workspace, default <data dir>/agent-files):
// the only local folder agent file transfers may touch. os.Root refuses paths and symlinks leading out.
func agentWorkspace() (*os.Root, string, error) {
	dir := filepath.Join(store.DataDir(), "agent-files")
	if s, err := store.LoadSettings(); err == nil && strings.TrimSpace(s.AgentWorkspace) != "" {
		dir = strings.TrimSpace(s.AgentWorkspace)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", failedErr("agent workspace: " + err.Error())
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", failedErr("agent workspace: " + err.Error())
	}
	return root, dir, nil
}

// workspacePath is the lexical check before any IO: a non-empty relative path inside the workspace.
func workspacePath(rel string) error {
	if rel == "" || !filepath.IsLocal(rel) {
		return deniedErr(rel + ": outside the agent workspace")
	}
	return nil
}

func (a *App) partialName() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seq++
	return ".rxt-partial-" + strconv.Itoa(a.seq)
}

func (a *App) filesGet(tab, remote, local string) (any, error) {
	if err := workspacePath(local); err != nil {
		return nil, err
	}
	root, dir, err := agentWorkspace()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	tmp := a.partialName()
	for _, name := range []string{local, tmp} { // validate (and claim) the target before any remote read
		f, err := root.Create(name)
		if err != nil {
			return nil, failedErr("agent workspace: " + err.Error())
		}
		f.Close()
	}
	n, err := func() (int64, error) {
		fs, err := a.agentFS(tab)
		if err != nil {
			return 0, err
		}
		n, err := fs.Download(remote, filepath.Join(dir, tmp))
		if err != nil {
			return 0, failedErr(err.Error())
		}
		return n, root.Rename(tmp, local)
	}()
	if err != nil {
		_ = root.Remove(tmp)
		_ = root.Remove(local)
		return nil, err
	}
	return map[string]any{"local": local, "bytes": n}, nil
}

func (a *App) filesPut(tab, local, remote string) (any, error) {
	if err := workspacePath(local); err != nil {
		return nil, err
	}
	root, dir, err := agentWorkspace()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	src, err := root.Open(local)
	if err != nil {
		return nil, failedErr("agent workspace: " + err.Error())
	}
	defer src.Close()
	// Upload takes a path, so the file opened inside the root is copied to a private temp name first.
	tmp := a.partialName()
	dst, err := root.Create(tmp)
	if err != nil {
		return nil, failedErr("agent workspace: " + err.Error())
	}
	defer root.Remove(tmp)
	_, err = io.Copy(dst, src)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, failedErr("agent workspace: " + err.Error())
	}
	fs, err := a.agentFS(tab)
	if err != nil {
		return nil, err
	}
	n, err := fs.Upload(filepath.Join(dir, tmp), remote)
	if err != nil {
		return nil, failedErr(err.Error())
	}
	return map[string]any{"remote": remote, "bytes": n}, nil
}

// describe is the audit target of an op: names, tab ids and paths, never input data or credentials.
func describe(op control.AgentOp) string {
	var tab uint64
	if op.Tab != nil {
		tab = *op.Tab
	}
	opt := func(b *bool) string {
		if b == nil {
			return "None"
		}
		return fmt.Sprintf("Some(%v)", *b)
	}
	switch op.Op {
	case "show_session", "open":
		if op.Session != nil && op.Session.ID != nil {
			return fmt.Sprintf("session %d", *op.Session.ID)
		}
		if op.Session != nil && op.Session.Name != nil {
			return *op.Session.Name
		}
	case "send", "wait", "close", "read", "screenshot", "serial_send_break":
		return fmt.Sprintf("tab %d", tab)
	case "serial_set_lines":
		return fmt.Sprintf("tab %d dtr=%s rts=%s", tab, opt(op.Dtr), opt(op.Rts))
	case "files_ls":
		return fmt.Sprintf("tab %d:%s", tab, op.Path)
	case "files_get":
		return fmt.Sprintf("tab %d:%s -> %s", tab, op.Remote, op.Local)
	case "files_put":
		return fmt.Sprintf("%s -> tab %d:%s", op.Local, tab, op.Remote)
	case "tunnel_start", "tunnel_stop", "gui_only":
		return op.Name
	}
	return ""
}

func trunc(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
