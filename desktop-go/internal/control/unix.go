//go:build linux || darwin

package control

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Unix-socket transport (Linux and macOS, Rust platform-linux / platform-macos control): a 0600
// socket in a private 0700 runtime directory, same-UID peers only (SO_PEERCRED / LOCAL_PEERCRED).
// RuntimeDir and peerUID are per OS (peer_linux.go, peer_darwin.go).

// socketName is the control socket file inside the runtime directory (matches the Rust SOCKET).
const socketName = "control.sock"

// otherUser is the reason a peer of another user is refused.
const otherUser = "agent control serves only the user running rustXterm"

// refusalLinger is how long a refused client gets to read its denied response.
const refusalLinger = time.Second

// Listener accepts same-user control connections. Dropping it (Close) removes the socket file.
type Listener struct {
	ln   *net.UnixListener
	path string
	uid  uint32
}

// Listen binds the control socket in dir (0700, owned by this user) as 0600. A live socket means
// another instance already serves agents (AlreadyExists); a stale one is replaced.
func Listen(dir string) (*Listener, error) {
	path := filepath.Join(dir, socketName)
	uid := uint32(os.Getuid())
	if base := filepath.Dir(dir); base != "" {
		_ = os.MkdirAll(base, 0o755)
	}
	if err := privateDir(dir, uid); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		if c, derr := net.Dial("unix", path); derr == nil {
			c.Close()
			return nil, fmt.Errorf("agent control socket %s is in use", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return &Listener{ln: ln, path: path, uid: uid}, nil
}

// Accept returns the next same-user connection, refusing (with a denied reason) peers of another user.
func (l *Listener) Accept() (Conn, error) {
	for {
		c, err := l.ln.AcceptUnix()
		if err != nil {
			return nil, err
		}
		puid, err := peerUID(c)
		if err == nil && puid == l.uid {
			return c, nil
		}
		go refuse(c) // another user (or unknown credentials)
	}
}

// Close stops listening and removes the socket file.
func (l *Listener) Close() error {
	err := l.ln.Close()
	_ = os.Remove(l.path)
	return err
}

// Connect dials the control socket in dir, requiring the server to be this same user.
func Connect(dir string) (Conn, error) {
	path := filepath.Join(dir, socketName)
	if _, err := os.Lstat(path); err != nil {
		return nil, errNotRunning
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		return nil, errNotRunning
	}
	uc, _ := c.(*net.UnixConn)
	if uc == nil {
		c.Close()
		return nil, errors.New("control socket is not a unix socket")
	}
	if puid, err := peerUID(uc); err != nil || puid != uint32(os.Getuid()) {
		c.Close()
		return nil, errors.New("control socket is served by another user")
	}
	return c, nil
}

// errNotRunning is the "no instance / nothing listening" sentinel (maps to CodeNotRunning).
var errNotRunning = errors.New("no running instance with agent control enabled")

// IsNotRunning reports whether err means there is no running instance to connect to.
func IsNotRunning(err error) bool { return errors.Is(err, errNotRunning) }

// Live reports whether a control socket exists, without connecting to it (so it does not take a
// listening instance). Used by the MCP server to choose the live vs offline tool set.
func Live() bool {
	dir, err := RuntimeDir()
	if err != nil {
		return false
	}
	_, err = os.Lstat(filepath.Join(dir, socketName))
	return err == nil
}

// privateDir creates dir as 0700 if missing, then requires a real directory owned by uid with no
// group/other permissions (never loosens or takes over an existing one). Mirrors the Rust check.
func privateDir(dir string, uid uint32) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: cannot read ownership", dir)
	}
	if st.Uid != uid {
		return fmt.Errorf("%s is owned by another user", dir)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is accessible by other users", dir)
	}
	return nil
}

// refuse tells a peer of another user why, then closes, draining its request (bounded) so it can read
// the answer after its write.
func refuse(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(refusalLinger))
	if err := WriteMessage(c, Err(0, CodeDenied, otherUser)); err == nil {
		_, _ = io.Copy(io.Discard, c)
	}
}
