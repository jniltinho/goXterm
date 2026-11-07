package wslconn

import (
	"reflect"
	"testing"
	"unicode/utf16"
)

// Same cases as the Rust crates/platform/src/wsl.rs tests.

func TestNamedDistributionUserAndCD(t *testing.T) {
	args, startup, err := LaunchArgs(Options{Distribution: "Debian", User: "dev", StartFolder: "/srv/app"}, true)
	if err != nil || !reflect.DeepEqual(args, []string{"-d", "Debian", "-u", "dev", "--cd", "/srv/app"}) || len(startup) != 0 {
		t.Fatalf("%v %v %v", args, startup, err)
	}
	if (Options{Distribution: "Debian"}).Label() != "Debian" || (Options{}).Label() != "Default" {
		t.Fatal("label")
	}
}

func TestDefaultDistributionHasNoFlag(t *testing.T) {
	if args, _, err := LaunchArgs(Options{}, true); err != nil || len(args) != 0 {
		t.Fatalf("%v %v", args, err)
	}
}

func TestStartupCommandsBecomeTypedLines(t *testing.T) {
	_, startup, err := LaunchArgs(Options{StartupCommands: "source .venv/bin/activate\nls \"$HOME\" | grep -c \"my file\""}, true)
	want := []string{"source .venv/bin/activate", `ls "$HOME" | grep -c "my file"`}
	if err != nil || !reflect.DeepEqual(startup, want) {
		t.Fatalf("%v %v", startup, err)
	}
}

func TestCDFallbackWithoutCDOption(t *testing.T) {
	line := func(f string) []string { _, s, _ := LaunchArgs(Options{StartFolder: f}, false); return s }
	if !reflect.DeepEqual(line("~"), []string{"cd"}) || !reflect.DeepEqual(line("~/src"), []string{"cd ~/'src'"}) ||
		!reflect.DeepEqual(line("/srv/app"), []string{"cd '/srv/app'"}) {
		t.Fatal("fallback lines")
	}
	if args, _, _ := LaunchArgs(Options{StartFolder: "/srv/app"}, false); len(args) != 0 {
		t.Fatalf("--cd used in fallback mode: %v", args)
	}
	if _, _, err := LaunchArgs(Options{StartFolder: "/a'b"}, false); err == nil {
		t.Fatal("folder with ' accepted in fallback mode")
	}
}

func TestInvalidNamesRefused(t *testing.T) {
	for _, o := range []Options{{Distribution: "Ubuntu & calc.exe"}, {Distribution: "Ubuntu", User: "9bad"}, {Distribution: "Ubuntu", User: "-bad"}} {
		if _, _, err := LaunchArgs(o, true); err == nil {
			t.Errorf("accepted %+v", o)
		}
	}
	for _, u := range []string{"Dev_User", "_service", "machine$"} {
		if ValidateUser(u) != nil {
			t.Errorf("refused %s", u)
		}
	}
}

func TestControlCharactersRefused(t *testing.T) {
	for _, bad := range []string{"a\rb", "a\x15b", "a\x1bb", "a\x7fb"} {
		if _, _, err := LaunchArgs(Options{StartFolder: bad}, true); err == nil {
			t.Errorf("folder %q", bad)
		}
		if _, _, err := LaunchArgs(Options{StartupCommands: bad}, true); err == nil {
			t.Errorf("command %q", bad)
		}
	}
}

func utf16le(s string, bom bool) []byte {
	var out []byte
	if bom {
		out = []byte{0xff, 0xfe}
	}
	for _, u := range utf16.Encode([]rune(s)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func TestParseList(t *testing.T) {
	d := ParseList([]byte("  NAME      STATE     VERSION\n* Ubuntu    Running   2\n  Debian    Stopped   2\n"))
	if len(d) != 2 || d[0] != (Distribution{"Ubuntu", true, 2}) || d[1] != (Distribution{"Debian", false, 2}) {
		t.Fatalf("utf-8: %+v", d)
	}
	pt := "  NOME      ESTADO    VERSÃO\n* Ubuntu    Em execução 2\n"
	if d := ParseList(utf16le(pt, true)); len(d) != 1 || d[0].Name != "Ubuntu" || d[0].Version != 2 {
		t.Fatalf("utf-16 bom: %+v", d)
	}
	if d := ParseList(utf16le(pt, false)); len(d) != 1 || d[0].Name != "Ubuntu" {
		t.Fatalf("utf-16 no bom: %+v", d)
	}
	if d := ParseList([]byte("  NAME  STATE  VERSION\n")); len(d) != 0 {
		t.Fatalf("header only: %+v", d)
	}
}
