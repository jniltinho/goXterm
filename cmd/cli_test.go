package cmd

// CLI parity tests: expected output and exit codes are the Rust CLI's (crates/agent cli.rs, lib.rs,
// engine offline.rs/control.rs, core quick_connect.rs), including the vectors of the Rust unit tests.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"rustxterm-go/internal/store"
)

// run executes the command line like Execute and returns stdout, stderr and the exit code.
func run(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()
	resetFlags(rootCmd)
	var out, errb bytes.Buffer
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	rootCmd.SetIn(strings.NewReader(stdin))
	code := report(rootCmd.ExecuteContext(context.Background()), &out, &errb)
	return out.String(), errb.String(), code
}

// resetFlags puts every flag back to its default (cobra keeps values between Execute calls).
func resetFlags(c *cobra.Command) {
	reset := func(f *pflag.Flag) { _ = f.Value.Set(f.DefValue); f.Changed = false }
	c.PersistentFlags().VisitAll(reset)
	c.Flags().VisitAll(reset)
	for _, s := range c.Commands() {
		resetFlags(s)
	}
}

// labData points the store at an empty temp data directory; with create, a store with the fictional
// master password "lab-master" exists there.
func labData(t *testing.T, create bool) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	os.Unsetenv("RXT_MASTER_PASSWORD")
	if create {
		if err := os.MkdirAll(store.DataDir(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := store.Create(store.DataDir(), "lab-master", nil); err != nil {
			t.Fatal(err)
		}
	}
}

// asTerminal stands in for an interactive terminal where the master password is typed.
func asTerminal(t *testing.T, password string) {
	t.Helper()
	isT, prompt := isTerminal, promptPassword
	isTerminal = func() bool { return true }
	promptPassword = func() (string, error) { return password, nil }
	t.Cleanup(func() { isTerminal, promptPassword = isT, prompt })
}

func expect(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

func expectCode(t *testing.T, args []string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%v: exit code %d, want %d", args, got, want)
	}
}

func TestPrintResultMatchesRust(t *testing.T) {
	// Rust cli.rs test session_line_has_no_secrets.
	v := map[string]any{"id": 3, "name": "prod", "protocol": "ssh", "host": "h", "port": 22, "user": "u", "password": "saved"}
	expect(t, "session line", line(v), "3\tprod\tssh://u@h:22")
	expect(t, "no user", line(map[string]any{"id": 4, "name": "n", "protocol": "vnc", "host": "h", "port": 5900}), "4\tn\tvnc://h:5900")
	cases := []struct {
		v      any
		asJSON bool
		want   string
	}{
		{"toml text", false, "toml text\n"},
		{"ends\n", false, "ends\n"},
		{[]map[string]any{{"id": 1, "name": "lab", "parent": nil}}, false, "{\"id\":1,\"name\":\"lab\",\"parent\":null}\n"},
		{[]map[string]any{v}, false, "3\tprod\tssh://u@h:22\n"},
		{map[string]any{"text": "uptime <1>"}, false, "uptime <1>\n"},
		{map[string]any{"removed": 7}, false, "{\n  \"removed\": 7\n}\n"},
		{map[string]any{"removed": 7, "a": []any{}}, true, "{\"a\":[],\"removed\":7}\n"},
		{[]map[string]any{}, true, "[]\n"},
	}
	for _, c := range cases {
		var b bytes.Buffer
		printResult(&b, c.v, c.asJSON)
		expect(t, "printResult", b.String(), c.want)
	}
}

func TestErrorsAndExitCodesMatchRust(t *testing.T) {
	// lib.rs exit_code: failed 1, usage 2, locked 3, denied 4, not_running 5, agent_control_off 6.
	for code, name := range map[int]string{1: "failed", 2: "usage", 3: "locked", 4: "denied", 5: "not_running", 6: "agent_control_off"} {
		if exitCodeOf(name) != code || codeNames[code] != name {
			t.Errorf("%s must be exit code %d", name, code)
		}
	}
	var out, errb bytes.Buffer
	flags.json = true
	code := report(usage("bad"), &out, &errb)
	flags.json = false
	expect(t, "json error", out.String(), "{\"error\":{\"code\":\"usage\",\"message\":\"bad\"}}\n")
	out.Reset()
	if report(denied("no"), &out, &errb) != 4 || code != 2 {
		t.Error("denied must exit 4, usage 2")
	}
	expect(t, "text error", errb.String(), "rustxterm: no\n")
}

func TestUsageErrorsExit2(t *testing.T) {
	for _, args := range [][]string{
		{"bogus"}, {"sessions"}, {"sessions", "nope"}, {"sessions", "show"}, {"sessions", "list", "--bogus"},
		{"send", "x", "hi"}, {"wait", "1", "p", "--timeout", "soon"}, {"serial", "lines", "1", "--dtr", "maybe"},
		{"sessions", "edit", "1", "--port", "70000"}, {"files", "ls"}, {"tabs", "list", "extra"},
	} {
		_, _, code := run(t, "", args...)
		expectCode(t, args, code, 2)
	}
}

func TestDurationsMatchRust(t *testing.T) {
	for in, want := range map[string]uint64{"10s": 10_000, "500ms": 500, "2m": 120_000, "3": 3000} {
		if got, err := parseDurationMs(in); err != nil || got != want {
			t.Errorf("%s: %d %v, want %d", in, got, err, want)
		}
	}
	if _, err := parseDurationMs("soon"); err == nil {
		t.Error("soon must be invalid")
	}
}

func TestQuickConnectMatchesRust(t *testing.T) {
	s, err := parseSessionURL("alice@lab.example:2222")
	if err != nil || s["protocol"] != "ssh" || s["host"] != "lab.example" || s["port"] != 2222 || s["user"] != "alice" || s["name"] != "alice@lab.example" {
		t.Errorf("plain ssh: %v %v", s, err)
	}
	for in, want := range map[string][3]any{
		"ssh://h": {"ssh", nil, 22}, "sftp://u@h": {"sftp", "u", 22}, "ftp://u@h": {"ftp", "u", 21}, "ftps://h:990": {"ftps", nil, 990},
		"rdp://h:3390": {"rdp", nil, 3390}, "vnc://h": {"vnc", nil, 5900}, "VNC://h/": {"vnc", nil, 5900},
		"telnet://h": {"telnet", nil, 23}, "telnet://u@h:2323": {"telnet", "u", 2323},
	} {
		s, err := parseSessionURL(in)
		if err != nil || s["protocol"] != want[0] || s["user"] != want[1] || s["port"] != want[2] || s["host"] != "h" {
			t.Errorf("%s: %v %v", in, s, err)
		}
	}
	if s, _ := parseSessionURL("[::1]:2200"); s["port"] != 2200 {
		t.Errorf("ipv6 port: %v", s)
	}
	if s, _ := parseSessionURL("root@[fe80::1]"); s["host"] != "fe80::1" {
		t.Errorf("ipv6 host: %v", s)
	}
	for _, bad := range []string{"", "ssh://", "ssh://u@", "@h", "telnet://", "telnet://u@", "h:0", "h:99999", "h:x", "a b", "[::1", "wsl://a b", "gopher://h"} {
		if _, err := parseSessionURL(bad); err == nil {
			t.Errorf("%q must be invalid", bad)
		}
	}
	if s, _ := parseSessionURL("wsl://"); s["name"] != "WSL" {
		t.Errorf("wsl default: %v", s)
	}
	if s, _ := parseSessionURL("WSL://Ubuntu-22.04/"); s["name"] != "Ubuntu-22.04" {
		t.Errorf("wsl named: %v", s)
	}
	s, _ = parseSessionURL("serial://ttyUSB0")
	expect(t, "serial name", str(s["name"]), "ttyUSB0 @ 115200")
	expect(t, "serial port", str(s["serial"].(map[string]any)["port"]), "/dev/ttyUSB0")
	s, _ = parseSessionURL("serial:///dev/ttyS1?baud=9600&data=7&parity=e&stop=2&flow=rtscts")
	expect(t, "serial line", encodeJSON(s["serial"], false),
		`{"line":{"baud":9600,"data_bits":"7","flow":"hardware","parity":"even","stop_bits":"2"},"port":"/dev/ttyS1"}`)
	for bad, key := range map[string]string{"serial:///dev/ttyUSB0?parity=q": "parity", "serial:///dev/ttyUSB0?baud=fast": "baud",
		"serial:///dev/ttyUSB0?baud=0": "baud", "serial:///dev/ttyUSB0?data=9": "data", "serial:///dev/ttyUSB0?stop=3": "stop",
		"serial:///dev/ttyUSB0?flow=x": "flow", "serial:///dev/ttyUSB0?speed=1": "speed", "serial://": "device"} {
		if _, err := parseSessionURL(bad); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s: %v must name %s", bad, err, key)
		}
	}
	expect(t, "redact", redactURL("ssh://bob:pw123@h"), "ssh://bob@h")
}

func TestOfflineUnlockRules(t *testing.T) {
	labData(t, false)
	_, errOut, code := run(t, "", "sessions", "list")
	expectCode(t, nil, code, 1)
	expect(t, "no database", errOut, "rustxterm: no database yet: create it in the rustXterm window first\n")

	labData(t, true)
	_, errOut, code = run(t, "", "folders", "list")
	expectCode(t, nil, code, 3)
	expect(t, "locked", errOut, "rustxterm: the database is locked: type the master password on a terminal, or enable the keyring\n")
	// Changes never take the environment or a pipe: only a terminal.
	t.Setenv("RXT_MASTER_PASSWORD", "lab-master")
	for _, args := range [][]string{{"sessions", "add", "ssh://h"}, {"sessions", "edit", "1", "--port", "2"}, {"sessions", "remove", "1"}} {
		_, errOut, code = run(t, "", args...)
		expectCode(t, args, code, 4)
		expect(t, "change needs a terminal", errOut, "rustxterm: offline changes require the master password typed on an interactive terminal\n")
	}
	_, errOut, code = run(t, "", "import", "mobaxterm-ini", "x.ini")
	expectCode(t, nil, code, 4)
	expect(t, "import needs a terminal", errOut, "rustxterm: import requires an interactive terminal\n")
	asTerminal(t, "wrong")
	if _, _, code = run(t, "", "sessions", "add", "ssh://h"); code != 3 {
		t.Errorf("wrong master password: exit %d, want 3", code)
	}
}

// Rust engine test offline_owner_changes_and_defaults, through the CLI.
func TestOfflineChangesMatchRust(t *testing.T) {
	labData(t, true)
	asTerminal(t, "lab-master")
	out, _, code := run(t, "", "sessions", "add", "ssh://bob@db.lab:2222", "--name", "db", "--folder", "lab/dbs")
	expectCode(t, nil, code, 0)
	expect(t, "add", out, "{\n  \"id\": 1,\n  \"name\": \"db\"\n}\n")
	out, _, _ = run(t, "", "folders", "list")
	if strings.Count(out, "\n") != 2 || !strings.Contains(out, `"name":"dbs"`) {
		t.Errorf("folders: %q", out)
	}
	out, _, _ = run(t, "", "sessions", "edit", "db", "--port", "2200", "--json")
	expect(t, "edit", out, "{\"id\":1,\"name\":\"db\"}\n")
	out, _, _ = run(t, "", "sessions", "list")
	expect(t, "list", out, "1\tdb\tssh://bob@db.lab:2200\n")
	out, _, _ = run(t, "", "sessions", "show", "1", "--json")
	for _, want := range []string{`"agent_policy":"deny"`, `"content_read":false`, `"password":"absent"`, `"port":2200`} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %s: %s", want, out)
		}
	}
	_, errOut, code := run(t, "", "sessions", "add", "ssh://bob:pw123@h")
	expectCode(t, nil, code, 2)
	expect(t, "credentials", errOut, "rustxterm: credentials are not accepted in session URLs\n")
	_, errOut, code = run(t, "", "sessions", "edit", "db", "--host", "a b")
	expectCode(t, nil, code, 2)
	expect(t, "bad host", errOut, "rustxterm: invalid host \"a b\"\n")
	run(t, "", "sessions", "add", "h2", "--name", "db")
	_, errOut, code = run(t, "", "sessions", "show", "db")
	expectCode(t, nil, code, 2)
	expect(t, "ambiguous", errOut, "rustxterm: several sessions are named \"db\"; use the id\n")
	_, errOut, _ = run(t, "", "sessions", "remove", "nope")
	expect(t, "unknown", errOut, "rustxterm: no session named \"nope\"\n")
	out, _, _ = run(t, "", "sessions", "remove", "2", "--json")
	expect(t, "remove", out, "{\"removed\":2}\n")
	_, errOut, code = run(t, "", "sessions", "show", "2")
	expectCode(t, nil, code, 2)
	expect(t, "removed id", errOut, "rustxterm: not found: session 2\n")
}

func TestExportAndImport(t *testing.T) {
	labData(t, true)
	asTerminal(t, "lab-master")
	ini := filepath.Join("..", "internal", "mobaini", "testdata", "basic.ini")
	_, errOut, code := run(t, "n\n", "import", "mobaxterm-ini", ini)
	expectCode(t, nil, code, 1)
	if !strings.Contains(errOut, "session(s) in 2 folder(s)") || !strings.HasSuffix(errOut, "rustxterm: import cancelled\n") {
		t.Errorf("preview/cancel: %q", errOut)
	}
	out, errOut, code := run(t, "y\n", "import", "mobaxterm-ini", ini, "--json")
	expectCode(t, nil, code, 0)
	if !strings.HasPrefix(out, `{"imported":10,"skipped":[{"line":`) || !strings.Contains(errOut, "  Production/web-01  ssh://web01.example:22\n") {
		t.Errorf("import: %q %q", out, errOut)
	}
	out, _, _ = run(t, "", "export")
	if !strings.HasPrefix(out, "format = \"rustxterm-export\"\nversion = 1\n") || !strings.Contains(out, "web01.example") || strings.Contains(out, "password") {
		t.Errorf("export toml: %q", out)
	}
	out, _, _ = run(t, "", "export", "--json")
	if !strings.HasPrefix(out, `{"folders":[`) || !strings.Contains(out, `"format":"rustxterm-export"`) || strings.Contains(out, "password") {
		t.Errorf("export json: %q", out)
	}
	file := filepath.Join(t.TempDir(), "export.toml")
	out, _, _ = run(t, "", "export", "--out", file)
	expect(t, "export --out", out, "{\n  \"path\": \""+file+"\"\n}\n")
	if b, err := os.ReadFile(file); err != nil || !strings.HasPrefix(string(b), "format = ") {
		t.Errorf("export file: %q %v", b, err)
	}
}

func TestCommandTreeMatchesRust(t *testing.T) {
	for _, path := range [][]string{
		{"sessions", "list"}, {"sessions", "show"}, {"sessions", "add"}, {"sessions", "edit"}, {"sessions", "remove"},
		{"folders", "list"}, {"tunnels", "list"}, {"export"}, {"import", "mobaxterm-ini"}, {"open"}, {"tabs", "list"},
		{"send"}, {"read"}, {"wait"}, {"screenshot"}, {"files", "ls"}, {"files", "get"}, {"files", "put"},
		{"tunnel", "start"}, {"tunnel", "stop"}, {"close"}, {"serial", "ports"}, {"serial", "break"}, {"serial", "lines"}, {"mcp"},
	} {
		if c, _, err := rootCmd.Find(path); err != nil || c.Name() != path[len(path)-1] {
			t.Errorf("%v not registered: %v", path, err)
		}
	}
}
