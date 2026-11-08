//go:build darwin

package control

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// RuntimeDir returns $TMPDIR/rustxterm-<uid> (the per-user temp dir launchd gives every session),
// matching the Rust platform-macos runtime directory.
func RuntimeDir() (string, error) {
	return filepath.Join(os.TempDir(), fmt.Sprintf("rustxterm-%d", os.Getuid())), nil
}

// peerUID reads the connected peer's effective UID via LOCAL_PEERCRED (what getpeereid uses).
func peerUID(c *net.UnixConn) (uint32, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Xucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if cerr != nil {
		return 0, cerr
	}
	return cred.Uid, nil
}
