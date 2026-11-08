//go:build linux

package cmd

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"rustxterm-go/internal/control"
)

// fakeInstance listens on the control socket and answers each connection with the next reply; the
// requests it received (as raw JSON objects) go to the returned channel. Rust client.rs fake::serve.
func fakeInstance(t *testing.T, replies ...control.Response) <-chan map[string]any {
	t.Helper()
	ln, err := control.Listen(filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "rustxterm"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got := make(chan map[string]any, len(replies))
	go func() {
		for _, r := range replies {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req map[string]any
			_, _ = control.ReadMessage(c, &req)
			got <- req
			_ = control.WriteMessage(c, r)
			c.Close()
		}
	}()
	return got
}

func TestLiveNotRunning(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	_, errOut, code := run(t, "", "tabs", "list")
	expectCode(t, nil, code, 5)
	expect(t, "not running", errOut, "rustxterm: rustXterm is not running, or agent control is off\n")
	out, _, _ := run(t, "", "close", "3", "--json")
	expect(t, "not running json", out, "{\"error\":{\"code\":\"not_running\",\"message\":\"rustXterm is not running, or agent control is off\"}}\n")
}

func TestLiveCommandsMatchRust(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	png := []byte("\x89PNG fake")
	ok := func(v any) control.Response { return control.OK(1, v) }
	cases := []struct {
		args    []string
		reply   control.Response
		request string // the request as the Rust client sends it (sorted keys)
		out     string
		code    int
	}{
		{[]string{"send", "2", "ls", "--enter"}, ok(map[string]any{"sent": 3}),
			`{"client":"rustxterm-cli","data":"ls\n","entry":"cli-live","id":1,"op":"send","tab":2}`, "{\n  \"sent\": 3\n}\n", 0},
		{[]string{"read", "2", "--lines", "5"}, ok(map[string]any{"text": "load average: 0.1"}),
			`{"client":"rustxterm-cli","entry":"cli-live","id":1,"lines":5,"op":"read","tab":2}`, "load average: 0.1\n", 0},
		{[]string{"wait", "2", "load average"}, ok(map[string]any{"matched": true}),
			`{"client":"rustxterm-cli","entry":"cli-live","id":1,"op":"wait","pattern":"load average","tab":2,"timeout_ms":10000}`, "{\n  \"matched\": true\n}\n", 0},
		{[]string{"open", "prod-web"}, ok(map[string]any{"tab": 4}),
			`{"client":"rustxterm-cli","entry":"cli-live","id":1,"op":"open","session":"prod-web"}`, "{\n  \"tab\": 4\n}\n", 0},
		{[]string{"open", "7", "--json"}, ok(map[string]any{"tab": 5}),
			`{"client":"rustxterm-cli","entry":"cli-live","id":1,"op":"open","session":7}`, "{\"tab\":5}\n", 0},
		{[]string{"tabs", "list"}, ok([]any{map[string]any{"id": 1, "title": "lab"}}),
			`{"client":"rustxterm-cli","entry":"cli-live","id":1,"op":"list_tabs"}`, "{\"id\":1,\"title\":\"lab\"}\n", 0},
		{[]string{"files", "ls", "3"}, ok(map[string]any{"path": "/home/alice", "entries": []any{}}),
			`{"client":"rustxterm-cli","entry":"cli-live","id":1,"op":"files_ls","path":".","tab":3}`, "{\n  \"entries\": [],\n  \"path\": \"/home/alice\"\n}\n", 0},
		{[]string{"files", "get", "3", "/var/log/syslog", "syslog.txt"}, ok(map[string]any{"bytes": 10}),
			`{"client":"rustxterm-cli","entry":"cli-live","id":1,"local":"syslog.txt","op":"files_get","remote":"/var/log/syslog","tab":3}`, "{\n  \"bytes\": 10\n}\n", 0},
		{[]string{"files", "put", "3", "a.txt", "/tmp/a.txt"}, ok(map[string]any{"bytes": 1}),
			`{"client":"rustxterm-cli","entry":"cli-live","id":1,"local":"a.txt","op":"files_put","remote":"/tmp/a.txt","tab":3}`, "{\n  \"bytes\": 1\n}\n", 0},
		{[]string{"tunnel", "start", "db-forward"}, control.Err(1, control.CodeDenied, "denied by policy"),
			`{"client":"rustxterm-cli","entry":"cli-live","id":1,"name":"db-forward","op":"tunnel_start"}`, "", 4},
		{[]string{"tunnel", "stop", "db-forward", "--json"}, control.Err(1, control.CodeAgentControlOff, "agent control is off"),
			`{"client":"rustxterm-cli","entry":"cli-live","id":1,"name":"db-forward","op":"tunnel_stop"}`,
			"{\"error\":{\"code\":\"agent_control_off\",\"message\":\"agent control is off\"}}\n", 6},
		{[]string{"close", "3"}, ok(map[string]any{"closed": 3}),
			`{"client":"rustxterm-cli","entry":"cli-live","id":1,"op":"close","tab":3}`, "{\n  \"closed\": 3\n}\n", 0},
		{[]string{"serial", "ports"}, ok([]any{"/dev/ttyUSB0"}),
			`{"client":"rustxterm-cli","entry":"cli-live","id":1,"op":"serial_list_ports"}`, "\"/dev/ttyUSB0\"\n", 0},
		{[]string{"serial", "break", "1", "--ms", "500"}, ok(map[string]any{"ok": true}),
			`{"client":"rustxterm-cli","entry":"cli-live","id":1,"ms":500,"op":"serial_send_break","tab":1}`, "{\n  \"ok\": true\n}\n", 0},
		{[]string{"serial", "lines", "1", "--dtr", "on", "--rts", "0"}, ok(map[string]any{"ok": true}),
			`{"client":"rustxterm-cli","dtr":true,"entry":"cli-live","id":1,"op":"serial_set_lines","rts":false,"tab":1}`, "{\n  \"ok\": true\n}\n", 0},
	}
	replies := make([]control.Response, len(cases))
	for i, c := range cases {
		replies[i] = c.reply
	}
	shot := control.OK(1, map[string]any{"png": base64.StdEncoding.EncodeToString(png)})
	got := fakeInstance(t, append(replies, shot)...)
	for _, c := range cases {
		out, _, code := run(t, "", c.args...)
		expectCode(t, c.args, code, c.code)
		expect(t, "request", encodeJSON(<-got, false), c.request)
		expect(t, "output", out, c.out)
	}

	// screenshot --out decodes the PNG into the file and prints its path.
	file := filepath.Join(t.TempDir(), "tab.png")
	out, _, code := run(t, "", "screenshot", "2", "--out", file, "--json")
	expectCode(t, nil, code, 0)
	expect(t, "screenshot request", encodeJSON(<-got, false), `{"client":"rustxterm-cli","entry":"cli-live","id":1,"op":"screenshot","tab":2}`)
	b, _ := json.Marshal(map[string]string{"path": file})
	expect(t, "screenshot", out, string(b)+"\n")
	if data, err := os.ReadFile(file); err != nil || string(data) != string(png) {
		t.Errorf("screenshot file: %q %v", data, err)
	}
}
