package gui

import (
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"

	"rustxterm-go/internal/store"
)

// sshHostKeyCallback verifies the server's host key against ~/.ssh/known_hosts and the stored
// ssh_trust table (keys the user accepted before, incl. in the Rust app). An unknown key raises a
// trust-on-first-use prompt in the SPA ("ssh:trust") and blocks until the user answers through
// SshTrustRespond — accept once, accept and save, or reject. RXT_SSH_INSECURE=1 accepts any key.
func (a *App) sshHostKeyCallback(host string, port int) ssh.HostKeyCallback {
	insecure := os.Getenv("RXT_SSH_INSECURE") == "1"
	known, kherr := store.KnownHostsCallback()
	var trusted []store.SSHTrustKey
	if st, err := openStore(); err == nil {
		trusted, _ = st.SSHTrustKeys(host, port)
		st.Close()
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if insecure {
			return nil
		}
		if kherr == nil && known(hostname, remote, key) == nil {
			return nil
		}
		algo := key.Type()
		keyB64 := base64.StdEncoding.EncodeToString(key.Marshal())
		for _, t := range trusted {
			if t.Algorithm == algo && t.KeyBase64 == keyB64 {
				return nil
			}
		}
		return a.promptTrust(host, port, key, algo, keyB64)
	}
}

func (a *App) promptTrust(host string, port int, key ssh.PublicKey, algo, keyB64 string) error {
	ch := make(chan string, 1)
	a.mu.Lock()
	a.seq++
	reqID := "trust" + strconv.Itoa(a.seq)
	if a.trust == nil {
		a.trust = map[string]chan string{}
	}
	a.trust[reqID] = ch
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.trust, reqID)
		a.mu.Unlock()
	}()

	if a.emit != nil {
		a.emit("ssh:trust", map[string]any{
			"reqId": reqID, "host": host, "port": port,
			"algorithm": algo, "fingerprint": ssh.FingerprintSHA256(key),
		})
	}
	select {
	case d := <-ch:
		switch d {
		case "once":
			return nil
		case "save":
			if st, err := openStore(); err == nil {
				_ = st.SaveSSHTrust(host, port, algo, keyB64)
				st.Close()
			}
			return nil
		default:
			return fmt.Errorf("host key not trusted for %s:%d (%s)", host, port, ssh.FingerprintSHA256(key))
		}
	case <-time.After(2 * time.Minute):
		return fmt.Errorf("host key trust prompt timed out for %s:%d", host, port)
	}
}

// SshTrustRespond delivers the user's decision ("once"/"save"/"reject") for a pending ssh:trust prompt.
func (a *App) SshTrustRespond(reqID, decision string) {
	a.mu.Lock()
	ch := a.trust[reqID]
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- decision:
		default:
		}
	}
}
