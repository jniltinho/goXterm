package gui

import (
	"log/slog"
	"net"
	"strings"

	"rustxterm-go/internal/control"
	"rustxterm-go/internal/store"
)

// startControl starts the agent control listener when the agent_control setting is on. It serves one
// request per connection over the same-user socket and never blocks Run; failures are logged, not
// fatal (migrate-gui-to-go-wails task 3.3). The Read group is served from the store; the live/write
// groups are enforced by the permission matrix and otherwise reported as not-yet-wired.
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
	log.Info("agent control listening on " + dir)
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

// serveControlConn reads one request, dispatches it and writes the response (the agent client uses one
// request per connection).
func (a *App) serveControlConn(c net.Conn, log *slog.Logger) {
	defer c.Close()
	var req control.Request
	if ok, err := control.ReadMessage(c, &req); !ok || err != nil {
		return
	}
	_ = control.WriteMessage(c, a.dispatch(req, log))
}

// dispatch enforces the permission matrix, then serves the request. A denied request is audited.
func (a *App) dispatch(req control.Request, log *slog.Logger) control.Response {
	group := control.Group(req.Op)
	if !control.Allowed(group, req.Entry) {
		log.Warn("agent control denied", "op", req.Op, "entry", req.Entry, "client", req.Client)
		return control.Err(req.ID, control.CodeDenied, req.Op+" is not permitted from "+req.Entry)
	}
	if group == control.GroupRead {
		return a.serveRead(req)
	}
	// Live, ContentRead and store writes over the socket are the next 3.3 increment (they need the
	// numeric tab registry / offline write path); the matrix above already gates them.
	return control.Err(req.ID, control.CodeFailed, req.Op+" is not implemented yet")
}

// serveRead serves the Read group (list/show) from the store; a locked store maps to CodeLocked.
func (a *App) serveRead(req control.Request) control.Response {
	if req.Op == "export" {
		return control.Err(req.ID, control.CodeFailed, "export is not implemented yet")
	}
	st, err := openStore()
	if err != nil {
		return control.Err(req.ID, control.CodeLocked, err.Error())
	}
	defer st.Close()
	switch req.Op {
	case "list_sessions":
		list, err := st.Sessions()
		return listOrErr(req.ID, list, err)
	case "list_folders":
		list, err := st.Folders()
		return listOrErr(req.ID, list, err)
	case "list_tunnels":
		list, err := st.Tunnels()
		return listOrErr(req.ID, list, err)
	case "show_session":
		list, err := st.Sessions()
		if err != nil {
			return control.Err(req.ID, control.CodeFailed, err.Error())
		}
		s := resolveSessionRef(list, req.Session)
		if s == nil {
			return control.Err(req.ID, control.CodeFailed, "no session matches the selector")
		}
		return control.OK(req.ID, s)
	}
	return control.Err(req.ID, control.CodeFailed, "unhandled read op "+req.Op)
}

func listOrErr(id uint64, list []map[string]any, err error) control.Response {
	if err != nil {
		return control.Err(id, control.CodeFailed, err.Error())
	}
	return control.OK(id, list)
}

// resolveSessionRef matches a session by id or exact name (case-insensitive); nil if none or no ref.
func resolveSessionRef(list []map[string]any, ref *control.SessionRef) map[string]any {
	if ref == nil {
		return nil
	}
	for _, s := range list {
		if ref.ID != nil && asInt64(s["id"]) == *ref.ID {
			return s
		}
		if ref.Name != nil && strings.EqualFold(asString(s["name"]), *ref.Name) {
			return s
		}
	}
	return nil
}
