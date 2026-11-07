package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestMCPServer drives the stdio server end to end: initialize, tools/list (no mutation tools, and
// live tools hidden with no instance), and tools/call for an absent mutation tool (rejected).
func TestMCPServer(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`, // notification: no reply
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"add_session","arguments":{"url":"ssh://h"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_sessions","arguments":{}}}`,
	}, "\n")

	var out bytes.Buffer
	if err := runMCP(strings.NewReader(in), &out); err != nil {
		t.Fatalf("runMCP: %v", err)
	}

	byID := map[string]rpcResp{}
	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		var r rpcResp
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("decode response %q: %v", sc.Text(), err)
		}
		byID[string(r.ID)] = r
	}
	if len(byID) != 4 {
		t.Fatalf("want 4 responses (the notification gets none), got %d", len(byID))
	}

	if r := byID["1"]; r.Error != nil || !hasKey(t, r.Result, "serverInfo") {
		t.Errorf("initialize: %+v", r)
	}

	// tools/list: read tools present, no mutation tool, and no live-only tool (no instance here).
	list, _ := json.Marshal(byID["2"].Result)
	s := string(list)
	if !strings.Contains(s, `"list_sessions"`) {
		t.Errorf("tools/list missing list_sessions: %s", s)
	}
	for _, absent := range []string{"add_session", "edit_session", "remove_session", "import", `"send_input"`, `"open_session"`} {
		if strings.Contains(s, absent) {
			t.Errorf("tools/list must not expose %s: %s", absent, s)
		}
	}

	// A mutation tool is absent, so calling it is rejected as an unknown tool (JSON-RPC error).
	if r := byID["3"]; r.Error == nil {
		t.Errorf("calling add_session must be rejected, got %+v", r)
	}

	// A read tool returns a tool result (not a protocol error) whether or not a store is unlocked.
	if r := byID["4"]; r.Error != nil || !hasKey(t, r.Result, "content") {
		t.Errorf("list_sessions call: %+v", r)
	}
}

func hasKey(t *testing.T, v any, key string) bool {
	t.Helper()
	b, _ := json.Marshal(v)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}
