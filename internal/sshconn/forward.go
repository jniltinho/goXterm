package sshconn

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Forwarding reports which forwarding the server accepted for this shell (banner ✓ / ✗).
func (s *Session) Forwarding() (x11, agentFwd bool) { return s.x11, s.agent }

// forwardAgent serves the server's auth-agent channels from the local ssh-agent and asks for
// forwarding on the shell's session.
func (s *Session) forwardAgent() error {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return errors.New("no local ssh-agent (SSH_AUTH_SOCK is not set)")
	}
	if err := agent.ForwardToRemote(s.client, sock); err != nil {
		return err
	}
	if err := agent.RequestAgentForwarding(s.sess); err != nil {
		return err
	}
	s.agent = true
	return nil
}

// StartupCommand is what runs instead of the plain login shell: the remote command, entered in
// startupDir when set (as the Rust app builds it); "" means the login shell.
func StartupCommand(remoteCommand, startupDir string) string {
	cmd := strings.TrimSpace(remoteCommand)
	switch {
	case startupDir == "":
		return cmd
	case cmd != "":
		return "cd -- " + shQuote(startupDir) + " && " + cmd
	default:
		return "cd -- " + shQuote(startupDir) + `; exec "${SHELL:-/bin/sh}" -l`
	}
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// x11Display is a parsed $DISPLAY: host "" means the local unix socket.
type x11Display struct {
	host           string
	number, screen int
}

func parseDisplay(s string) (x11Display, bool) {
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return x11Display{}, false
	}
	host, rest := s[:i], s[i+1:]
	num, scr := rest, "0"
	if j := strings.IndexByte(rest, '.'); j >= 0 {
		num, scr = rest[:j], rest[j+1:]
	}
	n, err1 := strconv.Atoi(num)
	sc, err2 := strconv.Atoi(scr)
	if err1 != nil || err2 != nil || n < 0 || sc < 0 {
		return x11Display{}, false
	}
	if host == "unix" {
		host = ""
	}
	return x11Display{host: host, number: n, screen: sc}, true
}

// dial connects to the local X server: the unix socket (macOS XQuartz puts a socket path in
// $DISPLAY), or TCP port 6000+N.
func (d x11Display) dial() (net.Conn, error) {
	switch {
	case strings.HasPrefix(d.host, "/"): // XQuartz: "/private/tmp/…/org.xquartz:0" is the socket path
		return net.Dial("unix", d.host+":"+strconv.Itoa(d.number))
	case d.host == "":
		return net.Dial("unix", fmt.Sprintf("/tmp/.X11-unix/X%d", d.number))
	default:
		return net.Dial("tcp", net.JoinHostPort(d.host, strconv.Itoa(6000+d.number)))
	}
}

const mitCookie = "MIT-MAGIC-COOKIE-1"

// forwardX11 requests X11 forwarding with a fake cookie and serves the server's x11 channels on
// the local display, swapping the fake cookie for the real one from Xauthority.
func (s *Session) forwardX11(log func(string)) error {
	d, ok := parseDisplay(os.Getenv("DISPLAY"))
	if !ok || (runtime.GOOS == "windows" && d.host == "") {
		if runtime.GOOS == "windows" {
			return errors.New("no local X server is configured (start VcXsrv or X410 and set DISPLAY=127.0.0.1:0)")
		}
		return errors.New("no usable $DISPLAY")
	}
	fake := make([]byte, 16)
	if _, err := rand.Read(fake); err != nil {
		return err
	}
	real := localCookie(d)
	if real == nil {
		log(fmt.Sprintf("no X11 cookie for display :%d; forwarding without authentication", d.number))
	}
	payload := ssh.Marshal(struct {
		Single   bool
		Protocol string
		Cookie   string
		Screen   uint32
	}{false, mitCookie, hex.EncodeToString(fake), uint32(d.screen)}) // #nosec G115 -- screen >= 0
	ok, err := s.sess.SendRequest("x11-req", true, payload)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("the server refused it (X11Forwarding no?)")
	}
	chans := s.client.HandleChannelOpen("x11")
	if chans == nil {
		return errors.New("already forwarding X11 on this connection")
	}
	go func() {
		for nc := range chans {
			ch, reqs, err := nc.Accept()
			if err != nil {
				continue
			}
			go ssh.DiscardRequests(reqs)
			go serveX11(ch, d, fake, real)
		}
	}()
	s.x11 = true
	return nil
}

// serveX11 checks the fake cookie in the client's connection setup, rewrites it with the real one
// and relays the channel to the local display.
func serveX11(ch ssh.Channel, d x11Display, fake, real []byte) {
	defer ch.Close()
	hdr := make([]byte, 12)
	if _, err := io.ReadFull(ch, hdr); err != nil {
		return
	}
	n, dl, ok := authLens(hdr)
	if !ok {
		return
	}
	rest := make([]byte, pad4(n)+pad4(dl))
	if _, err := io.ReadFull(ch, rest); err != nil {
		return
	}
	setup, ok := rewriteSetup(append(hdr, rest...), fake, real)
	if !ok {
		return // wrong or missing cookie: not ours
	}
	local, err := d.dial()
	if err != nil {
		return
	}
	defer local.Close()
	if _, err := local.Write(setup); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(local, ch); done <- struct{}{} }()
	go func() { _, _ = io.Copy(ch, local); _ = ch.CloseWrite(); done <- struct{}{} }()
	<-done
}

func pad4(n int) int { return (n + 3) &^ 3 }

// authLens returns the auth name and data lengths of an X11 connection setup header.
func authLens(h []byte) (n, d int, ok bool) {
	if len(h) < 12 {
		return 0, 0, false
	}
	var bo binary.ByteOrder
	switch h[0] {
	case 'B':
		bo = binary.BigEndian
	case 'l':
		bo = binary.LittleEndian
	default:
		return 0, 0, false
	}
	return int(bo.Uint16(h[6:])), int(bo.Uint16(h[8:])), true
}

// rewriteSetup checks the fake cookie in a full setup message and rebuilds it with real (or no
// auth when real is nil). ok is false when the cookie is wrong.
func rewriteSetup(setup, fake, real []byte) ([]byte, bool) {
	n, d, ok := authLens(setup)
	if !ok || len(setup) < 12+pad4(n)+d {
		return nil, false
	}
	name, data := setup[12:12+n], setup[12+pad4(n):12+pad4(n)+d]
	if string(name) != mitCookie || !bytes.Equal(data, fake) {
		return nil, false
	}
	newName, newData := []byte(mitCookie), real
	if real == nil {
		newName = nil
	}
	var bo binary.AppendByteOrder = binary.LittleEndian
	if setup[0] == 'B' {
		bo = binary.BigEndian
	}
	out := append([]byte(nil), setup[:6]...)
	out = bo.AppendUint16(out, uint16(len(newName))) // #nosec G115 -- 18 bytes
	out = bo.AppendUint16(out, uint16(len(newData))) // #nosec G115 -- a cookie
	out = append(out, 0, 0)
	for _, part := range [][]byte{newName, newData} {
		out = append(out, part...)
		out = append(out, make([]byte, pad4(len(out))-len(out))...)
	}
	return out, true
}

// localCookie reads the display's MIT-MAGIC-COOKIE-1 from $XAUTHORITY (or ~/.Xauthority).
func localCookie(d x11Display) []byte {
	path := os.Getenv("XAUTHORITY")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		path = filepath.Join(home, ".Xauthority")
	}
	data, err := os.ReadFile(path) // #nosec G304 -- the user's own Xauthority
	if err != nil {
		return nil
	}
	host, _ := os.Hostname()
	return findCookie(data, d.number, host)
}

// findCookie finds the MIT-MAGIC-COOKIE-1 for display in an Xauthority file: a FamilyWild or
// FamilyLocal entry for this host first, then any entry for the display.
func findCookie(data []byte, display int, hostname string) []byte {
	const local, wild = 256, 65535
	take := func() ([]byte, bool) {
		if len(data) < 2 {
			return nil, false
		}
		n := int(binary.BigEndian.Uint16(data))
		if len(data) < 2+n {
			return nil, false
		}
		f := data[2 : 2+n]
		data = data[2+n:]
		return f, true
	}
	number := strconv.Itoa(display)
	var fallback []byte
	for len(data) >= 2 {
		family := binary.BigEndian.Uint16(data)
		data = data[2:]
		addr, ok1 := take()
		num, ok2 := take()
		name, ok3 := take()
		cookie, ok4 := take()
		if !ok1 || !ok2 || !ok3 || !ok4 {
			break
		}
		if string(num) != number || string(name) != mitCookie {
			continue
		}
		if family == wild || (family == local && string(addr) == hostname) {
			return cookie
		}
		if fallback == nil {
			fallback = cookie
		}
	}
	return fallback
}
