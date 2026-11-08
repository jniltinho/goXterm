package mobaini

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// The Rust crates/store/tests/moba_ini.rs cases, on the same fictional fixtures (testdata/).

func load(t *testing.T, name string) Report {
	t.Helper()
	r, err := ImportFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func find(r Report, name string) map[string]any {
	for _, it := range r.Sessions {
		if it.Session["name"] == name {
			return it.Session
		}
	}
	return nil
}

func sub(m map[string]any, k string) map[string]any { v, _ := m[k].(map[string]any); return v }

func TestBasicTenSSHTwoFoldersOneSkippedRsh(t *testing.T) {
	r := load(t, "basic.ini")
	if len(r.Sessions) != 10 || !reflect.DeepEqual(r.Folders, []string{"Production", "Lab/Staging"}) {
		t.Fatalf("%d sessions, folders %v", len(r.Sessions), r.Folders)
	}
	n := map[string]int{}
	for _, it := range r.Sessions {
		n[it.Folder]++
		if it.Session["protocol"] != "ssh" {
			t.Fatalf("protocol %v", it.Session["protocol"])
		}
	}
	if n["Production"] != 5 || n["Lab/Staging"] != 5 {
		t.Fatalf("per folder %v", n)
	}
	if len(r.Skipped) != 1 || r.Skipped[0].Name != "Old rsh box" || r.Skipped[0].Line != 8 || !strings.Contains(r.Skipped[0].Reason, "rsh") {
		t.Fatalf("skipped %+v", r.Skipped)
	}
	db := find(r, "db-01")
	if db["host"] != "db01.example" || db["port"] != 2222 || db["user"] != "bob" {
		t.Fatalf("db-01 %v", db)
	}
	if _, ok := find(r, "stage-mon")["user"]; ok {
		t.Fatal("stage-mon has a user")
	}
	if s := sub(db, "ssh"); s["x11_forwarding"] != false || s["compression"] != false || s["jump_hosts"] != nil {
		t.Fatalf("db-01 ssh %v", s)
	}
}

func TestEachSupportedTypeAndSSHFields(t *testing.T) {
	r := load(t, "types.ini")
	ssh := find(r, "full-ssh")
	o := sub(ssh, "ssh")
	if ssh["host"] != "app.example" || ssh["port"] != 2022 || ssh["user"] != "alice" || o["x11_forwarding"] != true || o["compression"] != true {
		t.Fatalf("full-ssh %v", ssh)
	}
	if o["remote_command"] != "tmux attach; echo 50% #1 | cat" || o["key_file"] != `_ProfileDir_\.ssh\id_ed25519` {
		t.Fatalf("ssh fields %v", o)
	}
	hops := fmt.Sprint(o["jump_hosts"])
	want := fmt.Sprint([]map[string]any{
		{"host": "bastion.example", "port": 2200, "user": "bob", "key_file": `_ProfileDir_\.ssh\gw_key`},
		{"host": "hop2.example", "port": 22, "user": "carol"},
	})
	if hops != want {
		t.Fatalf("hops %s\nwant %s", hops, want)
	}
	if term := sub(ssh, "terminal"); term["font_family"] != "Hack" || term["font_size"] != 12.0 {
		t.Fatalf("terminal %v", term)
	}
	if sub(find(r, "files"), "terminal")["font_family"] != nil {
		t.Fatal("MobaFont kept")
	}
	for _, c := range []struct {
		name, proto, host string
		port              int
		user              any
	}{
		{"files", "sftp", "files.example", 22, "bob"}, {"ftp-site", "ftp", "ftp.example", 21, "alice"},
		{"ftps-explicit", "ftps", "secure.example", 21, "alice"}, {"ftps-implicit", "ftps", "secure.example", 990, "alice"},
		{"windows", "rdp", "win.example", 3390, "bob"}, {"desktop", "vnc", "vnc.example", 5901, nil},
		{"ssh-icon", "ssh", "icon.example", 22, "bob"},
	} {
		s := find(r, c.name)
		if s["protocol"] != c.proto || s["host"] != c.host || s["port"] != c.port || s["user"] != c.user {
			t.Errorf("%s: %v", c.name, s)
		}
	}
	if sub(find(r, "ftp-site"), "ftp")["startup_folder"] != "/pub" ||
		sub(find(r, "ftps-explicit"), "ftp")["tls"] != "explicit" || sub(find(r, "ftps-implicit"), "ftp")["tls"] != "implicit" {
		t.Fatal("ftp options")
	}
	if len(r.Skipped) != 2 || r.Skipped[0].Name != "rsh" || !strings.Contains(r.Skipped[1].Reason, "browser") {
		t.Fatalf("skipped %+v", r.Skipped)
	}
}

func warnings(r Report, name string) []string {
	var w []string
	for _, e := range r.Warnings {
		if e.Name == name {
			w = append(w, e.Reason)
		}
	}
	return w
}

func TestWSLBookmarks(t *testing.T) {
	r := load(t, "wsl.ini")
	if d := sub(find(r, "WSL-Alpine"), "wsl")["distribution"]; d != "Alpine" {
		t.Fatalf("alpine %v", d)
	}
	if d, ok := sub(find(r, "WSL-Default"), "wsl")["distribution"]; ok {
		t.Fatalf("default has %v", d)
	}
	full := sub(find(r, "wsl-full"), "wsl")
	if full["distribution"] != "Debian" || full["user"] != "dev" || full["start_folder"] != "/srv/app" || full["startup_commands"] != "source .venv/bin/activate" {
		t.Fatalf("wsl-full %v", full)
	}
	if sub(find(r, "wsl-desktop"), "wsl")["distribution"] != "Ubuntu" || !strings.Contains(strings.Join(warnings(r, "wsl-desktop"), ";"), "Full desktop") {
		t.Fatalf("desktop %v", warnings(r, "wsl-desktop"))
	}
	if len(warnings(r, "WSL-Alpine"))+len(warnings(r, "wsl-full")) != 0 {
		t.Fatal("unexpected warnings")
	}
}

func TestTelnetAndSerial(t *testing.T) {
	r := load(t, "telnet_serial.ini")
	if find(r, "jump-box")["protocol"] != "ssh" {
		t.Fatal("jump-box")
	}
	if rt := find(r, "router"); rt["protocol"] != "telnet" || rt["host"] != "router.example" || rt["port"] != 2323 || rt["user"] != "admin" || rt["telnet"] != nil {
		t.Fatalf("router %v", rt)
	}
	if sw := find(r, "switch"); sw["port"] != 23 || sw["user"] != nil {
		t.Fatalf("switch %v", sw)
	}
	com := sub(find(r, "console-com3"), "serial")
	if com["port"] != "COM3" || sub(com, "line")["baud"] != 9600 {
		t.Fatalf("com3 %v", com)
	}
	usb := sub(find(r, "console-usb"), "serial")
	want := map[string]any{"baud": 57600, "data_bits": "7", "parity": "even", "stop_bits": "2", "flow": "hardware"}
	if usb["port"] != "/dev/ttyUSB1" || !reflect.DeepEqual(sub(usb, "line"), want) {
		t.Fatalf("usb %v", usb)
	}
	odd := sub(find(r, "console-odd"), "serial")
	def := map[string]any{"baud": 115200, "data_bits": "8", "parity": "none", "stop_bits": "1", "flow": "none"}
	if odd["port"] != "/dev/ttyS0" || !reflect.DeepEqual(sub(odd, "line"), def) {
		t.Fatalf("odd %v", odd)
	}
	if w := warnings(r, "console-com3"); len(w) != 1 || !strings.Contains(w[0], "Windows port name COM3") {
		t.Fatalf("com3 warnings %v", w)
	}
	if len(warnings(r, "console-usb")) != 0 {
		t.Fatal("usb warnings")
	}
	ow := strings.Join(warnings(r, "console-odd"), ";")
	for _, what := range []string{"speed", "data bits", "parity", "stop bits", "flow control"} {
		if !strings.Contains(ow, what) {
			t.Errorf("odd warnings miss %s: %s", what, ow)
		}
	}
	if len(r.Skipped) != 1 || r.Skipped[0].Name != "no-port" || r.Skipped[0].Reason != "empty serial port" {
		t.Fatalf("skipped %+v", r.Skipped)
	}
}

func TestMalformedLinesSkippedWithLineNumbers(t *testing.T) {
	r := load(t, "malformed.ini")
	var names []string
	for _, it := range r.Sessions {
		names = append(names, it.Session["name"].(string))
	}
	if !reflect.DeepEqual(names, []string{"good-1", "good-2"}) || r.Sessions[1].Session["port"] != 22 {
		t.Fatalf("names %v", names)
	}
	var got []string
	for _, s := range r.Skipped {
		got = append(got, fmt.Sprintf("%d:%s", s.Line, s.Name))
	}
	if strings.Join(got, ",") != "6:short,7:,8:plain,9:badtype,10:badport,11:nohost,12:unknown" || !strings.Contains(r.Skipped[0].Reason, "fewer fields") {
		t.Fatalf("skipped %v %q", got, r.Skipped[0].Reason)
	}
}

func TestSecretsNeverRead(t *testing.T) {
	r := load(t, "secrets.ini")
	if len(r.Sessions) != 1 || strings.Contains(fmt.Sprintf("%+v", r), "FICTIONAL-SECRET") {
		t.Fatalf("%+v", r)
	}
}

func TestCRLFAndLatin1(t *testing.T) {
	r := load(t, "crlf.ini")
	if !reflect.DeepEqual(r.Folders, []string{"Café"}) || r.Sessions[0].Session["name"] != "réseau" || r.Sessions[0].Session["host"] != "net.example" || len(r.Skipped) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestSourceUnchanged(t *testing.T) {
	for _, n := range []string{"basic.ini", "types.ini", "malformed.ini", "secrets.ini", "crlf.ini", "telnet_serial.ini", "wsl.ini"} {
		before, _ := os.ReadFile("testdata/" + n)
		st, _ := os.Stat("testdata/" + n)
		load(t, n)
		after, _ := os.ReadFile("testdata/" + n)
		st2, _ := os.Stat("testdata/" + n)
		if !bytes.Equal(before, after) || !st.ModTime().Equal(st2.ModTime()) {
			t.Errorf("%s changed", n)
		}
	}
}
