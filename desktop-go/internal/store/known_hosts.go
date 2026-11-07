package store

import (
	"bufio"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// KnownHostsPath is the read-only OpenSSH known_hosts the Rust app also reads (~/.ssh/known_hosts).
func KnownHostsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}

// KnownHostsCallback parses ~/.ssh/known_hosts into an SSH host-key verifier. A missing file is not
// an error: it returns a callback that trusts nothing (every host is unknown), matching the Rust
// app's read-only use. Used by the SSH phase; here it also proves the file parses.
func KnownHostsCallback() (ssh.HostKeyCallback, error) {
	path := KnownHostsPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return func(string, net.Addr, ssh.PublicKey) error { return errors.New("host key not in known_hosts") }, nil
	}
	return knownhosts.New(path)
}

// KnownHostsCount counts host entries (non-empty, non-comment lines), for verification.
func KnownHostsCount() (int, error) {
	f, err := os.Open(KnownHostsPath())
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n++
	}
	return n, sc.Err()
}
