package tunnelconn

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestParseSocks5Request(t *testing.T) {
	type want struct {
		host string
		port uint16
		code byte
	}
	cases := []struct {
		in  []byte
		exp want
	}{
		{[]byte{5, 1, 0, 1, 10, 0, 0, 7, 0x1f, 0x90}, want{"10.0.0.7", 8080, 0}},
		{append([]byte{5, 1, 0, 3, 11}, append([]byte("db.internal"), 0x15, 0x38)...), want{"db.internal", 5432, 0}},
		{append([]byte{5, 1, 0, 4}, append(net.IPv6loopback, 0, 22)...), want{"::1", 22, 0}},
		{[]byte{5, 2, 0, 1, 1, 2, 3, 4, 0, 1}, want{code: socksCmdUnsupported}},
		{[]byte{5, 1, 0, 9, 1, 2, 3, 4, 0, 1}, want{code: socksATypUnsupported}},
		{[]byte{4, 1, 0, 1, 1, 2, 3, 4, 0, 1}, want{code: socksGeneralFailure}},
		{[]byte{5, 1, 0, 1, 1, 2}, want{code: socksGeneralFailure}},
	}
	for i, c := range cases {
		host, port, code := ParseSocks5Request(c.in)
		if code != c.exp.code || (code == 0 && (host != c.exp.host || port != c.exp.port)) {
			t.Errorf("case %d: got (%q,%d,%d) want (%q,%d,%d)", i, host, port, code, c.exp.host, c.exp.port, c.exp.code)
		}
	}
}

func TestEffectiveBindAddr(t *testing.T) {
	if got := EffectiveBindAddr("local", "0.0.0.0", false); got == "0.0.0.0" {
		t.Error("unconfirmed 0.0.0.0 must clamp to loopback")
	}
	if got := EffectiveBindAddr("remote", "0.0.0.0", false); got != "localhost" {
		t.Errorf("remote clamp = %q, want localhost", got)
	}
	if got := EffectiveBindAddr("local", "0.0.0.0", true); got != "0.0.0.0" {
		t.Errorf("confirmed = %q, want 0.0.0.0", got)
	}
	if got := EffectiveBindAddr("local", "127.0.0.1", false); got != "127.0.0.1" {
		t.Errorf("loopback = %q, want 127.0.0.1", got)
	}
}

// dialLab opens a real SSH client to the lab (gated on RXT_SSH_LAB; target from RXT_DEMO_SSH as
// user[:pass]@host[:port]) or skips the test.
func dialLab(t *testing.T) *ssh.Client {
	t.Helper()
	if os.Getenv("RXT_SSH_LAB") == "" {
		t.Skip("set RXT_SSH_LAB=1 and RXT_DEMO_SSH to run against the lab")
	}
	user, pass, host, port := parseTarget(os.Getenv("RXT_DEMO_SSH"))
	client, err := ssh.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)), &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // lab only
		Timeout:         10 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial lab: %v", err)
	}
	return client
}

// bannerThrough reads one line from a local address and checks it is an SSH banner.
func bannerThrough(t *testing.T, localPort int) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort)), 5*time.Second)
	if err != nil {
		t.Fatalf("dial local end: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read banner through tunnel: %v", err)
	}
	if !strings.HasPrefix(line, "SSH-2.0") {
		t.Fatalf("banner through tunnel = %q, want SSH-2.0…", line)
	}
}

// TestLabLocalForward forwards a local port to the remote's own sshd (127.0.0.1:22 inside the
// container) and reads the SSH banner through the tunnel — proving a local forward carries traffic.
func TestLabLocalForward(t *testing.T) {
	client := dialLab(t)
	defer client.Close()
	tun, err := Start(client, Spec{Kind: "local", BindAddr: "127.0.0.1", BindPort: 0, DestHost: "127.0.0.1", DestPort: 22})
	if err != nil {
		t.Fatalf("start tunnel: %v", err)
	}
	defer tun.Close()
	bannerThrough(t, tun.BoundPort)
}

// TestLabDynamicSocks starts a dynamic (SOCKS5) proxy and reaches the remote sshd through it, reading
// the banner — proving the SOCKS5 handshake + forward carry traffic.
func TestLabDynamicSocks(t *testing.T) {
	client := dialLab(t)
	defer client.Close()
	tun, err := Start(client, Spec{Kind: "dynamic", BindAddr: "127.0.0.1", BindPort: 0})
	if err != nil {
		t.Fatalf("start tunnel: %v", err)
	}
	defer tun.Close()

	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(tun.BoundPort)), 5*time.Second)
	if err != nil {
		t.Fatalf("dial SOCKS port: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	// SOCKS5 no-auth greeting, then CONNECT 127.0.0.1:22.
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil || reply[1] != 0 {
		t.Fatalf("SOCKS greeting reply = %v (err %v)", reply, err)
	}
	if _, err := conn.Write([]byte{5, 1, 0, 1, 127, 0, 0, 1, 0, 22}); err != nil {
		t.Fatal(err)
	}
	connectReply := make([]byte, 10)
	if _, err := io.ReadFull(conn, connectReply); err != nil || connectReply[1] != 0 {
		t.Fatalf("SOCKS CONNECT reply = %v (err %v)", connectReply, err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "SSH-2.0") {
		t.Fatalf("banner through SOCKS = %q (err %v)", line, err)
	}
}

// TestLabRemoteForward asks the remote to listen on a port and forward it to a local echo server, then
// connects to that port from the remote side (bash /dev/tcp) — proving a remote forward carries traffic.
func TestLabRemoteForward(t *testing.T) {
	client := dialLab(t)
	defer client.Close()

	// Local echo server: whatever it receives, it writes back.
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); c.Close() }()
		}
	}()
	echoPort := echo.Addr().(*net.TCPAddr).Port

	tun, err := Start(client, Spec{Kind: "remote", BindAddr: "127.0.0.1", BindPort: 0, DestHost: "127.0.0.1", DestPort: echoPort})
	if err != nil {
		t.Fatalf("start remote forward: %v", err)
	}
	defer tun.Close()
	if tun.BoundPort == 0 {
		t.Fatal("remote forward reported no bound port")
	}

	// From the remote, connect to the forwarded port and round-trip "ping" through the local echo.
	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	cmd := fmt.Sprintf("bash -c 'exec 3<>/dev/tcp/127.0.0.1/%d; printf ping >&3; head -c4 <&3'", tun.BoundPort)
	out, err := sess.CombinedOutput(cmd)
	if err != nil {
		t.Fatalf("remote round-trip: %v (out %q)", err, out)
	}
	if string(out) != "ping" {
		t.Fatalf("remote forward echo = %q, want ping", out)
	}
}

// parseTarget parses user[:password]@host[:port] (port defaults to 22); test-local copy.
func parseTarget(s string) (user, pass, host string, port int) {
	port = 22
	creds, rest := "", s
	if at := strings.LastIndexByte(s, '@'); at >= 0 {
		creds, rest = s[:at], s[at+1:]
	}
	if c := strings.IndexByte(creds, ':'); c >= 0 {
		user, pass = creds[:c], creds[c+1:]
	} else {
		user = creds
	}
	if c := strings.LastIndexByte(rest, ':'); c >= 0 {
		host = rest[:c]
		if p, err := strconv.Atoi(rest[c+1:]); err == nil {
			port = p
		}
	} else {
		host = rest
	}
	return
}
