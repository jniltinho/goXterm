//go:build !linux

package control

import (
	"errors"
	"net"
)

// shortcut: non-Linux transport stubs so the app cross-compiles. macOS gets the Unix-socket transport
// (getpeereid peer check) and Windows a named pipe — both migrate-gui-to-go-wails task 3.3.

var errUnsupported = errors.New("agent control is not available on this platform yet")

// Listener is the control listener; on this platform it is a stub that never starts.
type Listener struct{}

func (l *Listener) Accept() (net.Conn, error) { return nil, errUnsupported }
func (l *Listener) Close() error              { return nil }

// RuntimeDir / Listen / Connect report unsupported until the platform transport lands.
func RuntimeDir() (string, error)      { return "", errUnsupported }
func Listen(string) (*Listener, error) { return nil, errUnsupported }
func Connect(string) (net.Conn, error) { return nil, errUnsupported }

// IsNotRunning reports whether err means there is no running instance to connect to.
func IsNotRunning(err error) bool { return errors.Is(err, errUnsupported) }

// Live reports whether a control socket exists (always false until this platform's transport lands).
func Live() bool { return false }
