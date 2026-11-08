package gui

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rustxterm-go/internal/control"
	"rustxterm-go/internal/store"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func req(entry, op string, tab *uint64) control.Request {
	return control.Request{ID: 1, Client: "test", Entry: entry, AgentOp: control.AgentOp{Op: op, Tab: tab}}
}

func ptr[T any](v T) *T { return &v }

// TestDispatchMatrix checks the boundary before any work: GUI-only, wrong entry points and the matrix
// are denied, and a Read op passes the matrix and reaches the store (locked without one).
func TestDispatchMatrix(t *testing.T) {
	a := &App{}
	log := discardLog()
	r := a.dispatch(control.Request{ID: 1, Entry: control.EntryCliLive, AgentOp: control.AgentOp{Op: "gui_only", Name: "quit"}}, log)
	if r.Status != "err" || r.Code != control.CodeDenied {
		t.Errorf("gui_only from cli-live must be denied: %+v", r)
	}
	for _, entry := range []string{control.EntryMcpOffline, control.EntryCliOffline, control.EntryGui} {
		if r := a.dispatch(req(entry, "list_sessions", nil), log); r.Code != control.CodeDenied {
			t.Errorf("%s requests on the control channel must be denied: %+v", entry, r)
		}
	}
	if r := a.dispatch(control.Request{Entry: control.EntryMcpLive, AgentOp: control.AgentOp{Op: "add_session", URL: "ssh://h"}}, log); r.Code != control.CodeDenied {
		t.Errorf("store writes from mcp-live must be denied: %+v", r)
	}
	if r := a.dispatch(req(control.EntryMcpLive, "send", ptr(uint64(9))), log); r.Code != control.CodeUsage || r.Message != "no tab 9" {
		t.Errorf("send to an unknown tab: %+v", r)
	}
	r = a.dispatch(req(control.EntryMcpLive, "list_sessions", nil), log)
	if r.Status != "ok" && r.Code != control.CodeLocked {
		t.Errorf("list_sessions should be ok or locked, got %+v", r)
	}
}

// echo is a terminal whose input comes back as output, like a remote shell echoing.
type echo struct{ out func([]byte) }

func (e *echo) Write(p []byte) error  { e.out(p); return nil }
func (e *echo) Resize(int, int) error { return nil }
func (e *echo) Close() error          { return nil }

// fakeTab opens an echo terminal tab recorded as opened from session.
func fakeTab(a *App, session int64, protocol string) (string, uint64) {
	tab, onData, _ := a.newStreamTab()
	a.mu.Lock()
	a.term[tab] = &echo{onData}
	a.mu.Unlock()
	a.noteTab(tab, session, "s", protocol)
	return tab, tabNum(tab)
}

// labStore creates a throwaway store (fictional master password) with one session per policy and
// points openStore at it.
func labStore(t *testing.T) map[string]int64 {
	t.Helper()
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	t.Setenv("RXT_MASTER_PASSWORD", "lab-master")
	if err := store.Create(store.DataDir(), "lab-master", nil); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(store.DataDir(), "lab-master")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ids := map[string]int64{}
	for _, p := range []store.Policy{{Policy: "deny"}, {Policy: "ask"}, {Policy: "allow"}, {Policy: "allow", ContentRead: true}} {
		name := p.Policy
		if p.ContentRead {
			name += "-read"
		}
		js, _ := json.Marshal(map[string]any{"id": 0, "name": name, "protocol": "telnet", "host": "127.0.0.1", "port": 2323})
		id, err := st.SaveSession(string(js))
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetPolicy(id, p); err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	return ids
}

// TestAgentLiveOps drives send / wait / read / list_tabs / screenshot / close through the boundary on
// echo tabs, with the per-session policies from the store.
func TestAgentLiveOps(t *testing.T) {
	ids := labStore(t)
	a := &App{}
	log := discardLog()
	call := func(op control.AgentOp) control.Response {
		return a.dispatch(control.Request{ID: 1, Client: "test", Entry: control.EntryMcpLive, AgentOp: op}, log)
	}
	_, readTab := fakeTab(a, ids["allow-read"], "telnet")
	_, allowTab := fakeTab(a, ids["allow"], "telnet")
	_, denyTab := fakeTab(a, ids["deny"], "telnet")
	_, quickTab := fakeTab(a, 0, "terminal") // a quick-connect tab has no policy
	_, localTab := fakeTab(a, 0, "local")

	r := call(control.AgentOp{Op: "send", Tab: &readTab, Data: "uptime\r\n"})
	if r.Status != "ok" || !strings.Contains(string(r.Result), `"sent_to":[`) {
		t.Fatalf("send: %+v", r)
	}
	r = call(control.AgentOp{Op: "wait", Tab: &readTab, Pattern: "re:up.ime", TimeoutMs: ptr(uint64(1000))})
	if r.Status != "ok" || !strings.Contains(string(r.Result), `"text":"uptime`) {
		t.Errorf("wait with content read: %+v", r)
	}
	if r = call(control.AgentOp{Op: "read", Tab: &readTab, Lines: ptr(uint32(5))}); r.Status != "ok" || !strings.Contains(string(r.Result), "uptime") {
		t.Errorf("read: %+v", r)
	}
	if r = call(control.AgentOp{Op: "screenshot", Tab: &readTab}); r.Status != "ok" || !strings.Contains(string(r.Result), `"format":"text"`) {
		t.Errorf("terminal screenshot: %+v", r)
	}

	// allow without content read: send and wait work, the matched text is withheld, read is denied.
	call(control.AgentOp{Op: "send", Tab: &allowTab, Data: "id\n"})
	if r = call(control.AgentOp{Op: "wait", Tab: &allowTab, Pattern: "id", TimeoutMs: ptr(uint64(1000))}); r.Status != "ok" || !strings.Contains(string(r.Result), `"text":""`) {
		t.Errorf("wait without content read must hide the text: %+v", r)
	}
	if r = call(control.AgentOp{Op: "read", Tab: &allowTab}); r.Code != control.CodeDenied || r.Message != "content read is not allowed for this session" {
		t.Errorf("read without content read: %+v", r)
	}
	if r = call(control.AgentOp{Op: "wait", Tab: &allowTab, Pattern: "never", TimeoutMs: ptr(uint64(100))}); r.Code != control.CodeFailed {
		t.Errorf("wait timeout: %+v", r)
	}
	for tab, msg := range map[uint64]string{denyTab: "denied by policy", quickTab: "denied by policy", localTab: localDenied} {
		if r = call(control.AgentOp{Op: "send", Tab: &tab, Data: "x"}); r.Code != control.CodeDenied || r.Message != msg {
			t.Errorf("send to tab %d: %+v, want %q", tab, r, msg)
		}
	}
	if r = call(control.AgentOp{Op: "serial_send_break", Tab: &readTab}); r.Code != control.CodeUsage {
		t.Errorf("break on a telnet tab: %+v", r)
	}

	r = call(control.AgentOp{Op: "list_tabs"})
	var tabs []map[string]any
	if err := json.Unmarshal(r.Result, &tabs); err != nil || len(tabs) != 5 || tabs[0]["protocol"] != "telnet" {
		t.Errorf("list_tabs: %s %v", r.Result, err)
	}
	if r = call(control.AgentOp{Op: "close", Tab: &allowTab}); r.Status != "ok" {
		t.Errorf("close: %+v", r)
	}
	if _, _, ok := a.tabByNum(allowTab); ok {
		t.Error("a closed tab is still listed")
	}

	// Store reads: show_session carries the policy; export has no password markers.
	r = call(control.AgentOp{Op: "show_session", Session: &control.SessionRef{Name: ptr("ask")}})
	if r.Status != "ok" || !strings.Contains(string(r.Result), `"agent_policy":"ask"`) {
		t.Errorf("show_session: %+v", r)
	}
	if r = call(control.AgentOp{Op: "export"}); r.Status != "ok" || strings.Contains(string(r.Result), `"password"`) || !strings.Contains(string(r.Result), `"format"`) {
		t.Errorf("export: %+v", r)
	}
	if r = call(control.AgentOp{Op: "open", Session: &control.SessionRef{Name: ptr("deny")}}); r.Message != "denied by policy" {
		t.Errorf("open a deny session: %+v", r)
	}
}

// TestAgentApproval: an ask session's write needs the user's yes; no answer, a no, or no window deny it.
func TestAgentApproval(t *testing.T) {
	ids := labStore(t)
	a := &App{}
	_, tab := fakeTab(a, ids["ask"], "telnet")
	send := control.Request{ID: 1, Client: "test", Entry: control.EntryCliLive, AgentOp: control.AgentOp{Op: "send", Tab: &tab, Data: "ls\n"}}

	if r := a.dispatch(send, discardLog()); r.Code != control.CodeDenied {
		t.Errorf("no window to ask in must deny: %+v", r)
	}
	for _, yes := range []bool{true, false} {
		asked := make(chan map[string]any, 1)
		a.emit = func(name string, data any) {
			if name == "agent:approval" {
				asked <- data.(map[string]any)
			}
		}
		go func() {
			p := <-asked
			if p["command"] != "send" || p["target"] != describe(send.AgentOp) {
				t.Errorf("approval prompt %+v", p)
			}
			a.AgentApprove(p["id"].(uint64), yes)
		}()
		r := a.dispatch(send, discardLog())
		if yes != (r.Status == "ok") {
			t.Errorf("approve=%v: %+v", yes, r)
		}
	}
	a.AgentApprove(99, true) // an unknown or answered approval is ignored
}

// TestWorkspacePaths: agent file transfers stay inside the workspace.
func TestWorkspacePaths(t *testing.T) {
	for _, bad := range []string{"", "/etc/passwd", "../x", "a/../../x"} {
		if err := workspacePath(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	if err := workspacePath("dir/file.txt"); err != nil {
		t.Error(err)
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root, dir, err := agentWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("workspace %s: %v %v", dir, fi, err)
	}
	if err := os.Symlink("/etc", filepath.Join(dir, "link")); err == nil {
		if _, err := root.Open("link/passwd"); err == nil {
			t.Error("a symlink out of the workspace must not be followed")
		}
	}
}

// TestControlSocketRoundTrip drives the dispatcher over a real control socket end to end.
func TestControlSocketRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rustxterm")
	ln, err := control.Listen(dir)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	a := &App{}
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		a.serveControlConn(c, discardLog())
	}()
	c, err := control.Connect(dir)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()
	for id := uint64(7); id < 9; id++ { // several requests on one connection
		r := control.Request{ID: id, Client: "t", Entry: control.EntryCliLive, AgentOp: control.AgentOp{Op: "gui_only", Name: "quit"}}
		if err := control.WriteMessage(c, r); err != nil {
			t.Fatal(err)
		}
		var resp control.Response
		if ok, err := control.ReadMessage(c, &resp); !ok || err != nil {
			t.Fatalf("read response: ok=%v err=%v", ok, err)
		}
		if resp.ID != id || resp.Status != "err" || resp.Code != control.CodeDenied {
			t.Fatalf("gui_only over the socket must be denied: %+v", resp)
		}
	}
}
