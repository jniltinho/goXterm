//go:build lab

package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rustxterm-go/internal/gui"
	"rustxterm-go/internal/store"
)

// TestLabMCP drives a live SSH session over MCP against a window-less app serving agent control:
// open the lab sshd session (alice / lab-alice-pass on 127.0.0.1:2222, fictional credentials), run a
// command, wait for and read its output, list and close the tab — and get the policy refusal for a
// deny session. Run: RXT_SSH_LAB=1 go test -tags lab -run TestLabMCP ./cmd (lab up, host network).
func TestLabMCP(t *testing.T) {
	if os.Getenv("RXT_SSH_LAB") == "" {
		t.Skip("set RXT_SSH_LAB=1 with the lab sshd up (127.0.0.1:2222)")
	}
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(base, "run"))
	t.Setenv("RXT_MASTER_PASSWORD", "lab-master")
	t.Setenv("RXT_SSH_INSECURE", "1") // throwaway lab host key
	if err := os.MkdirAll(filepath.Join(base, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(store.DataDir(), "lab-master", nil); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(store.DataDir(), "lab-master")
	if err != nil {
		t.Fatal(err)
	}
	for name, policy := range map[string]string{"lab-ssh": "allow", "lab-deny": "deny"} {
		js := fmt.Sprintf(`{"id":0,"name":%q,"protocol":"ssh","host":"127.0.0.1","port":2222,"user":"alice","ssh":{"use_agent":false}}`, name)
		id, err := st.SaveSession(js)
		if err == nil {
			err = st.SetCredential(id, store.CredPassword, []byte("lab-alice-pass"))
		}
		if err == nil {
			err = st.SetPolicy(id, store.Policy{Policy: policy, ContentRead: true})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	st.Close()
	if err := os.MkdirAll(store.ConfigDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.ConfigDir(), "config.toml"), []byte("agent_control = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop := gui.StartAgentControlForLab(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer stop()

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() { _ = runMCP(inR, outW); outW.Close() }()
	defer inW.Close()
	lines := bufio.NewScanner(outR)
	lines.Buffer(make([]byte, 0, 1<<20), 1<<20)
	seq := 0
	rpc := func(method string, params any) rpcResp {
		t.Helper()
		seq++
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": seq, "method": method, "params": params})
		if _, err := inW.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
		if !lines.Scan() {
			t.Fatalf("%s: no response", method)
		}
		var r rpcResp
		if err := json.Unmarshal(lines.Bytes(), &r); err != nil || r.Error != nil {
			t.Fatalf("%s: %s %v", method, lines.Text(), err)
		}
		return r
	}
	// tool returns the tool's text and whether it is an error result.
	tool := func(name string, args map[string]any) (string, bool) {
		t.Helper()
		var res struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		}
		b, _ := json.Marshal(rpc("tools/call", map[string]any{"name": name, "arguments": args}).Result)
		_ = json.Unmarshal(b, &res)
		return res.Content[0].Text, res.IsError
	}

	rpc("initialize", map[string]any{})
	if b, _ := json.Marshal(rpc("tools/list", nil).Result); !strings.Contains(string(b), `"open_session"`) {
		t.Fatalf("live tools hidden with an instance running: %s", b)
	}
	text, isErr := tool("open_session", map[string]any{"session": "lab-ssh"})
	var opened struct{ Tab uint64 }
	if isErr || json.Unmarshal([]byte(text), &opened) != nil || opened.Tab == 0 {
		t.Fatalf("open_session: %s", text)
	}
	tab := opened.Tab
	if text, isErr = tool("send_input", map[string]any{"tab": tab, "text": "echo rxt-$((40+2))\n"}); isErr {
		t.Fatalf("send_input: %s", text)
	}
	if text, isErr = tool("wait_for_output", map[string]any{"tab": tab, "pattern": "rxt-42", "timeout_ms": 15000}); isErr || !strings.Contains(text, "rxt-42") {
		t.Fatalf("wait_for_output: %s", text)
	}
	if text, isErr = tool("read_terminal", map[string]any{"tab": tab, "lines": 20}); isErr || !strings.Contains(text, "rxt-42") {
		t.Errorf("read_terminal: %s", text)
	}
	if text, isErr = tool("list_tabs", map[string]any{}); isErr || !strings.Contains(text, `"protocol":"ssh"`) {
		t.Errorf("list_tabs: %s", text)
	}
	if text, isErr = tool("file_list", map[string]any{"tab": tab, "path": "~"}); isErr || !strings.Contains(text, `"entries"`) {
		t.Errorf("file_list over the tab's SSH connection: %s", text)
	}
	if text, isErr = tool("open_session", map[string]any{"session": "lab-deny"}); !isErr || text != "denied by policy" {
		t.Errorf("open a deny session: %q (error=%v)", text, isErr)
	}
	if text, isErr = tool("close_tab", map[string]any{"tab": tab}); isErr {
		t.Errorf("close_tab: %s", text)
	}
	if text, _ = tool("list_tabs", map[string]any{}); text != "[]" {
		t.Errorf("tab still listed after close: %s", text)
	}
}
