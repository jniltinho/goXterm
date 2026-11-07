package gui

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"rustxterm-go/internal/control"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestDispatchMatrix checks the permission matrix is enforced before any work: GUI-only and
// matrix-forbidden ops are denied, allowed-but-unwired live ops report failed, and a Read op passes
// the matrix and reaches the store (locked without a master password).
func TestDispatchMatrix(t *testing.T) {
	a := &App{}
	log := discardLog()

	r := a.dispatch(control.Request{ID: 1, Entry: control.EntryCliLive, AgentOp: control.AgentOp{Op: "gui_only", Name: "quit"}}, log)
	if r.Status != "err" || r.Code != control.CodeDenied {
		t.Errorf("gui_only from cli-live must be denied: %+v", r)
	}
	r = a.dispatch(control.Request{ID: 2, Entry: control.EntryMcpOffline, AgentOp: control.AgentOp{Op: "send"}}, log)
	if r.Code != control.CodeDenied {
		t.Errorf("send from mcp-offline must be denied by the matrix: %+v", r)
	}
	r = a.dispatch(control.Request{ID: 3, Entry: control.EntryMcpLive, AgentOp: control.AgentOp{Op: "send"}}, log)
	if r.Code != control.CodeFailed {
		t.Errorf("send from mcp-live is allowed by the matrix but unwired → failed: %+v", r)
	}
	// Read op passes the matrix and reaches the store: ok with a store, locked without one — never denied.
	r = a.dispatch(control.Request{ID: 4, Entry: control.EntryMcpLive, AgentOp: control.AgentOp{Op: "list_sessions"}}, log)
	if r.Code == control.CodeDenied {
		t.Errorf("list_sessions must pass the matrix, got denied: %+v", r)
	}
	if r.Status != "ok" && r.Code != control.CodeLocked {
		t.Errorf("list_sessions should be ok or locked, got %+v", r)
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
	req := control.Request{ID: 7, Client: "t", Entry: control.EntryCliLive, AgentOp: control.AgentOp{Op: "gui_only", Name: "quit"}}
	if err := control.WriteMessage(c, req); err != nil {
		t.Fatal(err)
	}
	var resp control.Response
	if ok, err := control.ReadMessage(c, &resp); !ok || err != nil {
		t.Fatalf("read response: ok=%v err=%v", ok, err)
	}
	if resp.ID != 7 || resp.Status != "err" || resp.Code != control.CodeDenied {
		t.Fatalf("gui_only over the socket must be denied: %+v", resp)
	}
}
