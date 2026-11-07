package cmd

import (
	"fmt"

	"rustxterm-go/internal/control"
)

// param is one MCP tool parameter; kind is "string" or "integer".
type param struct {
	name     string
	kind     string
	desc     string
	required bool
}

// toolDef is one MCP tool. wireOp is the control op discriminant (differs from the tool name for the
// live tools, matching the Rust agent). liveOnly tools appear only when an instance is reachable.
type toolDef struct {
	name     string
	wireOp   string
	desc     string
	params   []param
	liveOnly bool
}

var (
	pSession = param{"session", "string", "Session id or exact name", true}
	pTab     = param{"tab", "integer", "Tab id", true}
)

// mcpTools mirrors the Rust agent's TOOLS: read-only store tools plus live tools. There are no
// session add/edit/remove/import tools — store writes are not reachable from MCP (permission matrix).
var mcpTools = []toolDef{
	{"list_sessions", "list_sessions", "List saved sessions (no credentials)", nil, false},
	{"show_session", "show_session", "Show one saved session", []param{pSession}, false},
	{"list_folders", "list_folders", "List session folders", nil, false},
	{"list_tunnels", "list_tunnels", "List saved tunnels", nil, false},
	{"export", "export", "Export sessions, folders and tunnels (no credentials)", nil, false},
	{"open_session", "open", "Open a saved session in a new tab", []param{pSession}, true},
	{"list_tabs", "list_tabs", "List open tabs", nil, true},
	{"read_terminal", "read", "Read a tab's terminal text", []param{pTab, {"lines", "integer", "Last N lines", false}}, true},
	{"send_input", "send", "Send text to a tab", []param{pTab, {"text", "string", "Text to send", true}}, true},
	{"wait_for_output", "wait", "Wait until a tab's output matches a pattern", []param{pTab, {"pattern", "string", "Substring to wait for", true}, {"timeout_ms", "integer", "Timeout in ms (default 10000)", false}}, true},
	{"screenshot_tab", "screenshot", "Capture a tab as a PNG image", []param{pTab}, true},
	{"file_list", "files_ls", "List files on a tab", []param{pTab, {"path", "string", "Remote path (default .)", false}}, true},
	{"file_get", "files_get", "Download a file from a tab into the agent workspace", []param{pTab, {"remote", "string", "Remote path", true}, {"local", "string", "Local path", true}}, true},
	{"file_put", "files_put", "Upload an agent-workspace file to a tab", []param{pTab, {"local", "string", "Local path", true}, {"remote", "string", "Remote path", true}}, true},
	{"tunnel_start", "tunnel_start", "Start a saved tunnel", []param{{"name", "string", "Tunnel name", true}}, true},
	{"tunnel_stop", "tunnel_stop", "Stop a saved tunnel", []param{{"name", "string", "Tunnel name", true}}, true},
	{"close_tab", "close", "Close a tab", []param{pTab}, true},
	{"serial_list_ports", "serial_list_ports", "List local serial ports", nil, true},
	{"serial_send_break", "serial_send_break", "Send a break on a serial tab", []param{pTab, {"ms", "integer", "Break length in ms (default 250)", false}}, true},
	{"serial_set_lines", "serial_set_lines", "Set DTR and/or RTS on a serial tab", []param{pTab, {"dtr", "integer", "1 = on, 0 = off", false}, {"rts", "integer", "1 = on, 0 = off", false}}, true},
}

// toolByName returns the tool definition, or nil.
func toolByName(name string) *toolDef {
	for i := range mcpTools {
		if mcpTools[i].name == name {
			return &mcpTools[i]
		}
	}
	return nil
}

// jsonSchema builds the JSON Schema for a tool's parameters.
func (d *toolDef) jsonSchema() map[string]any {
	props := map[string]any{}
	var required []string
	for _, p := range d.params {
		s := map[string]any{"description": p.desc}
		if p.kind == "integer" {
			s["type"], s["minimum"] = "integer", 0
		} else {
			s["type"] = "string"
		}
		props[p.name] = s
		if p.required {
			required = append(required, p.name)
		}
	}
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

// buildOp validates args against the tool and builds its control op.
func (d *toolDef) buildOp(args map[string]any) (control.AgentOp, error) {
	for k := range args {
		if !d.hasParam(k) {
			return control.AgentOp{}, fmt.Errorf("unknown parameter %q for tool %s", k, d.name)
		}
	}
	op := control.AgentOp{Op: d.wireOp}
	str := func(n string) (string, error) {
		v, ok := args[n].(string)
		if !ok || v == "" {
			return "", fmt.Errorf("missing or invalid string parameter %q", n)
		}
		return v, nil
	}
	u64 := func(n string) (*uint64, error) {
		raw, present := args[n]
		if !present || raw == nil {
			return nil, nil
		}
		f, ok := raw.(float64)
		if !ok || f < 0 {
			return nil, fmt.Errorf("parameter %q must be a non-negative integer", n)
		}
		v := uint64(f)
		return &v, nil
	}
	onOff := func(n string) (*bool, error) {
		p, err := u64(n)
		if err != nil || p == nil {
			return nil, err
		}
		if *p > 1 {
			return nil, fmt.Errorf("parameter %q must be 0 or 1", n)
		}
		b := *p == 1
		return &b, nil
	}
	var err error
	switch d.name {
	case "show_session", "open_session":
		s, e := str("session")
		if e != nil {
			return op, e
		}
		op.Session = &control.SessionRef{Name: &s}
	case "read_terminal":
		if op.Tab, err = reqTab(args); err != nil {
			return op, err
		}
		lines, e := u64("lines")
		if e != nil {
			return op, e
		}
		if lines != nil {
			n := uint32(*lines)
			op.Lines = &n
		}
	case "send_input":
		if op.Tab, err = reqTab(args); err != nil {
			return op, err
		}
		if op.Data, err = str("text"); err != nil {
			return op, err
		}
	case "wait_for_output":
		if op.Tab, err = reqTab(args); err != nil {
			return op, err
		}
		if op.Pattern, err = str("pattern"); err != nil {
			return op, err
		}
		t, e := u64("timeout_ms")
		if e != nil {
			return op, e
		}
		d := uint64(10000)
		if t != nil {
			d = *t
		}
		op.TimeoutMs = &d
	case "screenshot_tab", "close_tab":
		op.Tab, err = reqTab(args)
	case "file_list":
		if op.Tab, err = reqTab(args); err != nil {
			return op, err
		}
		if p, ok := args["path"].(string); ok && p != "" {
			op.Path = p
		} else {
			op.Path = "."
		}
	case "file_get":
		if op.Tab, err = reqTab(args); err != nil {
			return op, err
		}
		if op.Remote, err = str("remote"); err != nil {
			return op, err
		}
		op.Local, err = str("local")
	case "file_put":
		if op.Tab, err = reqTab(args); err != nil {
			return op, err
		}
		if op.Local, err = str("local"); err != nil {
			return op, err
		}
		op.Remote, err = str("remote")
	case "tunnel_start", "tunnel_stop":
		op.Name, err = str("name")
	case "serial_send_break":
		if op.Tab, err = reqTab(args); err != nil {
			return op, err
		}
		op.Ms, err = u64("ms")
	case "serial_set_lines":
		if op.Tab, err = reqTab(args); err != nil {
			return op, err
		}
		if op.Dtr, err = onOff("dtr"); err != nil {
			return op, err
		}
		op.Rts, err = onOff("rts")
	}
	return op, err
}

func (d *toolDef) hasParam(name string) bool {
	for _, p := range d.params {
		if p.name == name {
			return true
		}
	}
	return false
}

func reqTab(args map[string]any) (*uint64, error) {
	f, ok := args["tab"].(float64)
	if !ok || f < 0 {
		return nil, fmt.Errorf("missing or invalid integer parameter \"tab\"")
	}
	v := uint64(f)
	return &v, nil
}

// offlineRead serves a Read-group op from the store (no running instance); used as the MCP fallback.
func offlineRead(op control.AgentOp) (any, error) {
	if op.Op == "export" {
		return nil, fmt.Errorf("export is not implemented yet")
	}
	st, err := openStore()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	switch op.Op {
	case "list_sessions":
		return st.Sessions()
	case "list_folders":
		return st.Folders()
	case "list_tunnels":
		return st.Tunnels()
	case "show_session":
		list, err := st.Sessions()
		if err != nil {
			return nil, err
		}
		ref := ""
		if op.Session != nil && op.Session.Name != nil {
			ref = *op.Session.Name
		} else if op.Session != nil && op.Session.ID != nil {
			ref = fmt.Sprint(*op.Session.ID)
		}
		s := resolveSession(list, ref)
		if s == nil {
			return nil, fmt.Errorf("no session matches %q", ref)
		}
		return s, nil
	}
	return nil, fmt.Errorf("%s needs a running instance with agent control", op.Op)
}
