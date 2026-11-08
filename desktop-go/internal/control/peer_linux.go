//go:build linux

package control

import (
	"errors"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// RuntimeDir returns $XDG_RUNTIME_DIR/rustxterm, where the control socket lives.
func RuntimeDir() (string, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" || !filepath.IsAbs(base) {
		return "", errors.New("no runtime directory ($XDG_RUNTIME_DIR) for agent control")
	}
	return filepath.Join(base, "rustxterm"), nil
}

// peerUID reads the connected peer's UID via SO_PEERCRED.
func peerUID(c *net.UnixConn) (uint32, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var ucred *unix.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		ucred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if cerr != nil {
		return 0, cerr
	}
	return ucred.Uid, nil
}
