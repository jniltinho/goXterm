package control

import "testing"

// The policy decisions of the Rust engine's authorize (engine/src/tests/agent.rs).
func TestAuthorize(t *testing.T) {
	deny := &Policy{Policy: "deny", ContentRead: true}
	ask := &Policy{Policy: "ask"}
	askRead := &Policy{Policy: "ask", ContentRead: true}
	allow := &Policy{Policy: "allow"}
	allowRead := &Policy{Policy: "allow", ContentRead: true}
	cases := []struct {
		entry, op string
		policy    *Policy
		want      Decision
		msg       string
	}{
		// GUI-only and the matrix come first, whatever the policy.
		{EntryGui, "list_sessions", allowRead, Deny, "list_sessions is GUI-only"},
		{EntryMcpLive, "gui_only", allowRead, Deny, "gui_only is GUI-only"},
		{EntryMcpLive, "add_session", allowRead, Deny, "add_session is not allowed from mcp-live"},
		{EntryMcpOffline, "open", allowRead, Deny, "open is not allowed from mcp-offline"},
		{EntryCliOffline, "edit_session", nil, Allow, ""}, // offline owner ops: matrix only
		// Store reads and session-less live ops need no policy.
		{EntryMcpLive, "export", nil, Allow, ""},
		{EntryMcpLive, "list_tabs", nil, Allow, ""},
		{EntryCliLive, "serial_list_ports", nil, Allow, ""},
		// Session ops: no session, deny, unknown values.
		{EntryMcpLive, "open", nil, Deny, "no such session"},
		{EntryMcpLive, "open", deny, Deny, "denied by policy"},
		{EntryMcpLive, "send", &Policy{Policy: "root"}, Deny, "denied by policy"},
		// allow runs everything except content reads without the flag.
		{EntryMcpLive, "open", allow, Allow, ""},
		{EntryMcpLive, "send", allow, Allow, ""},
		{EntryMcpLive, "wait", allow, Allow, ""},
		{EntryMcpLive, "read", allow, Deny, "content read is not allowed for this session"},
		{EntryMcpLive, "files_get", allow, Deny, "content read is not allowed for this session"},
		{EntryMcpLive, "read", allowRead, Allow, ""},
		{EntryMcpLive, "files_put", allowRead, Allow, ""},
		// ask: write effects need an approval, reads do not.
		{EntryCliLive, "open", ask, NeedsApproval, ""},
		{EntryCliLive, "send", ask, NeedsApproval, ""},
		{EntryCliLive, "close", ask, NeedsApproval, ""},
		{EntryCliLive, "tunnel_stop", ask, NeedsApproval, ""},
		{EntryCliLive, "serial_set_lines", ask, NeedsApproval, ""},
		{EntryCliLive, "wait", ask, Allow, ""},
		{EntryCliLive, "screenshot", ask, Deny, "content read is not allowed for this session"},
		{EntryCliLive, "screenshot", askRead, Allow, ""},
		{EntryCliLive, "files_get", askRead, NeedsApproval, ""},
	}
	for _, c := range cases {
		got, msg := Authorize(c.entry, c.op, c.policy)
		if got != c.want || msg != c.msg {
			t.Errorf("Authorize(%s, %s, %+v) = %v %q, want %v %q", c.entry, c.op, c.policy, got, msg, c.want, c.msg)
		}
	}
}
