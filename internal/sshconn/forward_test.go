package sshconn

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func TestStartupCommand(t *testing.T) {
	cases := []struct{ cmd, dir, want string }{
		{"", "", ""},
		{"", "/tmp/it's here", `cd -- '/tmp/it'\''s here'; exec "${SHELL:-/bin/sh}" -l`},
		{"uname -a", "/tmp/it's here", `cd -- '/tmp/it'\''s here' && uname -a`},
		{" uname -a ", "", "uname -a"},
	}
	for _, c := range cases {
		if got := StartupCommand(c.cmd, c.dir); got != c.want {
			t.Errorf("StartupCommand(%q, %q) = %q, want %q", c.cmd, c.dir, got, c.want)
		}
	}
}

func TestParseDisplay(t *testing.T) {
	for in, want := range map[string]x11Display{
		":0": {"", 0, 0}, "unix:1.2": {"", 1, 2}, "127.0.0.1:10.0": {"127.0.0.1", 10, 0},
		"/private/tmp/com.apple.launchd.x/org.xquartz:0": {"/private/tmp/com.apple.launchd.x/org.xquartz", 0, 0},
	} {
		if got, ok := parseDisplay(in); !ok || got != want {
			t.Errorf("parseDisplay(%q) = %+v %v, want %+v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "nohost", ":x", ":1.y"} {
		if _, ok := parseDisplay(bad); ok {
			t.Errorf("parseDisplay(%q) accepted", bad)
		}
	}
}

// x11Setup builds an X11 connection setup with the given auth name and data.
func x11Setup(big bool, name, data []byte) []byte {
	var bo binary.AppendByteOrder = binary.LittleEndian
	order := byte('l')
	if big {
		bo, order = binary.BigEndian, 'B'
	}
	b := []byte{order, 0}
	b = bo.AppendUint16(b, 11)
	b = bo.AppendUint16(b, 0)
	b = bo.AppendUint16(b, uint16(len(name)))
	b = bo.AppendUint16(b, uint16(len(data)))
	b = append(b, 0, 0)
	for _, p := range [][]byte{name, data} {
		b = append(b, p...)
		b = append(b, make([]byte, pad4(len(b))-len(b))...)
	}
	return b
}

func TestRewriteSetup(t *testing.T) {
	fake, real := bytes.Repeat([]byte{1}, 16), bytes.Repeat([]byte{2}, 16)
	for _, big := range []bool{false, true} {
		got, ok := rewriteSetup(x11Setup(big, []byte(mitCookie), fake), fake, real)
		if !ok || !bytes.Equal(got, x11Setup(big, []byte(mitCookie), real)) {
			t.Fatalf("big=%v: real cookie not substituted", big)
		}
		got, ok = rewriteSetup(x11Setup(big, []byte(mitCookie), fake), fake, nil)
		if !ok || !bytes.Equal(got, x11Setup(big, nil, nil)) {
			t.Fatalf("big=%v: no-auth rewrite wrong", big)
		}
		if _, ok := rewriteSetup(x11Setup(big, []byte(mitCookie), real), fake, real); ok {
			t.Fatalf("big=%v: wrong cookie accepted", big)
		}
	}
}

// xauthEntry is one Xauthority record.
func xauthEntry(family uint16, addr, num string, cookie []byte) []byte {
	b := binary.BigEndian.AppendUint16(nil, family)
	for _, f := range [][]byte{[]byte(addr), []byte(num), []byte(mitCookie), cookie} {
		b = binary.BigEndian.AppendUint16(b, uint16(len(f)))
		b = append(b, f...)
	}
	return b
}

func TestFindCookie(t *testing.T) {
	other, mine, wild := []byte("other-host"), []byte("mine"), []byte("wild")
	data := append(append(xauthEntry(256, "elsewhere", "0", other), xauthEntry(256, "box", "0", mine)...),
		xauthEntry(256, "box", "1", wild)...)
	if got := findCookie(data, 0, "box"); !bytes.Equal(got, mine) {
		t.Fatalf("local entry: %q", got)
	}
	if got := findCookie(data, 0, "nohost"); !bytes.Equal(got, other) {
		t.Fatalf("fallback: %q", got)
	}
	if got := findCookie(append(data, xauthEntry(65535, "", "2", wild)...), 2, "x"); !bytes.Equal(got, wild) {
		t.Fatalf("wild: %q", got)
	}
	if findCookie(data, 9, "box") != nil {
		t.Fatal("cookie for an unknown display")
	}
}

// run connects with cfg (a remote command), collects the output and waits for the command to end.
func run(t *testing.T, cfg Config) string {
	t.Helper()
	var mu sync.Mutex
	var out strings.Builder
	done := make(chan struct{})
	cfg.HostKey, cfg.Cols, cfg.Rows = ssh.InsecureIgnoreHostKey(), 120, 24
	s, err := Connect(cfg, func(b []byte) { mu.Lock(); out.Write(b); mu.Unlock() }, func(error) { close(done) })
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer s.Close()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("remote command did not end")
	}
	mu.Lock()
	defer mu.Unlock()
	return out.String()
}

func lab(t *testing.T) {
	if os.Getenv("RXT_SSH_LAB") == "" {
		t.Skip("set RXT_SSH_LAB=1 with the lab sshd up (127.0.0.1:2222)")
	}
}

// TestLabJumpHostEnv reaches the target through a gateway (the lab sshd jumping to its own
// 127.0.0.1:22), with an environment variable (AcceptEnv FOO) and a remote command.
func TestLabJumpHostEnv(t *testing.T) {
	lab(t)
	pw := func() (string, error) { return "lab-alice-pass", nil }
	out := run(t, Config{
		Host: "127.0.0.1", Port: 22, User: "alice", AskPassword: pw,
		Jumps: []Jump{{Host: "127.0.0.1", Port: 2222, User: "alice", HostKey: ssh.InsecureIgnoreHostKey(), AskPassword: pw}},
		Env:   [][2]string{{"FOO", "rxt-env-ok"}}, RemoteCommand: `echo "via=$SSH_CONNECTION foo=$FOO"`,
	})
	if !strings.Contains(out, "foo=rxt-env-ok") || !strings.Contains(out, "via=127.0.0.1 ") {
		t.Fatalf("output %q: want FOO and a connection from 127.0.0.1 (through the gateway)", out)
	}
}

// TestLabAgentForwarding forwards an in-process agent holding one key; `ssh-add -l` on the server
// lists it.
func TestLabAgentForwarding(t *testing.T) {
	lab(t)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kr := agent.NewKeyring()
	if err := kr.Add(agent.AddedKey{PrivateKey: priv, Comment: "rxt-agent-test"}); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "agent.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { _ = agent.ServeAgent(kr, c); c.Close() }()
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", sock)
	out := run(t, Config{Host: "127.0.0.1", Port: 2222, User: "alice", Password: "lab-alice-pass",
		AgentForwarding: true, RemoteCommand: "ssh-add -l"})
	if !strings.Contains(out, "rxt-agent-test") {
		t.Fatalf("ssh-add -l on the server = %q", out)
	}
}

// TestLabX11Forwarding runs xdpyinfo on the server; its connection reaches a fake local X server
// (127.0.0.1:6077) whose setup carries the real cookie from a test Xauthority, not the fake one.
func TestLabX11Forwarding(t *testing.T) {
	lab(t)
	real := bytes.Repeat([]byte{0x5a}, 16)
	xa := filepath.Join(t.TempDir(), "Xauthority")
	if err := os.WriteFile(xa, xauthEntry(65535, "", "77", real), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XAUTHORITY", xa)
	t.Setenv("DISPLAY", "127.0.0.1:77")
	l, err := net.Listen("tcp", "127.0.0.1:6077")
	if err != nil {
		t.Skipf("fake X server port: %v", err)
	}
	defer l.Close()
	got := make(chan []byte, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
		buf := make([]byte, 12+20+16)
		n, _ := io.ReadFull(c, buf)
		got <- buf[:n]
	}()
	out := run(t, Config{Host: "127.0.0.1", Port: 2222, User: "alice", Password: "lab-alice-pass",
		X11: true, RemoteCommand: "timeout 5 xdpyinfo >/dev/null 2>&1; echo x11-done"})
	select {
	case setup := <-got:
		if want := x11Setup(setup[0] == 'B', []byte(mitCookie), real); !bytes.Equal(setup, want) {
			t.Fatalf("setup at the local X server = % x\nwant % x", setup, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("no X11 connection reached the local display; output %q", out)
	}
}
