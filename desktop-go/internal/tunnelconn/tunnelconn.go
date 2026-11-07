// Package tunnelconn is the SSH port-forwarding engine (pure Go, golang.org/x/crypto/ssh): local,
// remote and dynamic (SOCKS5) forwards over an existing *ssh.Client. Loopback by default — a
// non-loopback bind needs ExposureConfirmed (migrate-gui-to-go-wails phase 11 — tunnels).
package tunnelconn

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Spec is one forward. Kind is "local", "remote" or "dynamic" (SOCKS5). DestHost/DestPort are unused
// for dynamic (the SOCKS client names the destination per connection).
type Spec struct {
	Kind              string
	BindAddr          string
	BindPort          int
	DestHost          string
	DestPort          int
	ExposureConfirmed bool
}

// EffectiveBindAddr returns BindAddr for loopback or confirmed exposure; otherwise it clamps to
// loopback (remote forwards to "localhost", local/dynamic to "127.0.0.1"), matching the Rust app.
func EffectiveBindAddr(kind, bindAddr string, exposureConfirmed bool) string {
	switch bindAddr {
	case "127.0.0.1", "::1", "localhost":
		return bindAddr
	}
	if exposureConfirmed {
		return bindAddr
	}
	if kind == "remote" {
		return "localhost"
	}
	return "127.0.0.1"
}

// Tunnel is a running forward. Close stops it (the listener closes, the accept loop ends).
type Tunnel struct {
	ln   net.Listener
	once sync.Once
	// BoundPort is the port actually in use (equals BindPort unless it was 0 and the OS/server chose).
	BoundPort int
}

// Start opens the listener and spawns the accept loop. For local/dynamic the listener is local and
// each connection is dialed through client; for remote the server listens and each connection is
// dialed to the local destination.
func Start(client *ssh.Client, s Spec) (*Tunnel, error) {
	addr := net.JoinHostPort(EffectiveBindAddr(s.Kind, s.BindAddr, s.ExposureConfirmed), strconv.Itoa(s.BindPort))
	var ln net.Listener
	var err error
	if s.Kind == "remote" {
		ln, err = client.Listen("tcp", addr)
	} else {
		ln, err = net.Listen("tcp", addr)
	}
	if err != nil {
		return nil, bindError(s.BindPort, err)
	}
	t := &Tunnel{ln: ln, BoundPort: portOf(ln.Addr(), s.BindPort)}
	go t.accept(client, s)
	return t, nil
}

func (t *Tunnel) accept(client *ssh.Client, s Spec) {
	for {
		conn, err := t.ln.Accept()
		if err != nil {
			return // listener closed (Close) or fatal accept error
		}
		go t.handle(client, s, conn)
	}
}

func (t *Tunnel) handle(client *ssh.Client, s Spec, local net.Conn) {
	defer local.Close()
	switch s.Kind {
	case "remote":
		// A connection arrived on the server; carry it to the local destination.
		dst, err := net.Dial("tcp", net.JoinHostPort(s.DestHost, strconv.Itoa(s.DestPort)))
		if err != nil {
			return
		}
		pipe(local, dst)
	case "dynamic":
		host, port, err := socks5Handshake(local)
		if err != nil {
			return
		}
		remote, err := client.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
		if err != nil {
			_, _ = local.Write(socks5Reply(socksHostUnreachable))
			return
		}
		_, _ = local.Write(socks5Reply(0))
		pipe(local, remote)
	default: // local
		remote, err := client.Dial("tcp", net.JoinHostPort(s.DestHost, strconv.Itoa(s.DestPort)))
		if err != nil {
			return
		}
		pipe(local, remote)
	}
}

// Close stops the tunnel; safe to call more than once.
func (t *Tunnel) Close() error {
	var err error
	t.once.Do(func() { err = t.ln.Close() })
	return err
}

// pipe copies bytes both ways until either side closes, then closes both.
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) { _, _ = io.Copy(dst, src); done <- struct{}{} }
	go cp(a, b)
	go cp(b, a)
	<-done
	a.Close()
	b.Close()
	<-done
}

func portOf(addr net.Addr, fallback int) int {
	if ta, ok := addr.(*net.TCPAddr); ok {
		return ta.Port
	}
	return fallback
}

func bindError(port int, e error) error {
	return fmt.Errorf("cannot listen on port %d: %w", port, e)
}
