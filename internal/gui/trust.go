package gui

import (
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"rustxterm-go/internal/store"
	"rustxterm-go/internal/tlstrust"
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
	d, err := a.askTrust(map[string]any{"host": host, "port": port, "algorithm": algo, "fingerprint": ssh.FingerprintSHA256(key)})
	if err != nil {
		return fmt.Errorf("host key trust prompt timed out for %s:%d", host, port)
	}
	switch d {
	case "once":
		return nil
	case "save":
		if st, err := openStore(); err == nil {
			_ = st.SaveSSHTrust(host, port, algo, keyB64)
			st.Close()
		}
		return nil
	}
	return fmt.Errorf("host key not trusted for %s:%d (%s)", host, port, ssh.FingerprintSHA256(key))
}

// tlsVerifier is the TLS trust decision for RDP and FTPS (tlstrust): system-trusted certificates
// pass, a certificate pinned in cert_trust passes, anything else asks with the same trust dialog as
// SSH host keys ("Accept and save" pins it, in the Rust format).
func (a *App) tlsVerifier(host string, port int) func(tls.ConnectionState) error {
	pinned, ask, save := a.certTrust(host, port)
	return tlstrust.Verifier(host, pinned, ask, save)
}

// legacyKeyVerifier is the trust decision on an RDP server key under standard (legacy) RDP
// security, pinned in cert_trust with the "RSA " prefix like the Rust app (tlstrust.LegacyVerifier).
func (a *App) legacyKeyVerifier(host string, port int) func(fingerprint string) error {
	pinned, ask, save := a.certTrust(host, port)
	return tlstrust.LegacyVerifier(net.JoinHostPort(host, strconv.Itoa(port)), pinned, ask, save)
}

// certTrust is the pinned fingerprint of host:port and the trust dialog / pin store for it.
func (a *App) certTrust(host string, port int) (string, tlstrust.Ask, func(string)) {
	pinned := ""
	if st, err := openStore(); err == nil {
		pinned, _, _ = st.CertTrust(host, port)
		st.Close()
	}
	ask := func(q tlstrust.Question) (bool, error) {
		fp, kind := "SHA256:"+q.Fingerprint, "tls"
		if strings.HasPrefix(q.Fingerprint, tlstrust.LegacyKeyPrefix) {
			fp, kind = q.Fingerprint, "legacy"
		}
		d, err := a.askTrust(map[string]any{"host": host, "port": port, "kind": kind, "algorithm": q.Subject,
			"fingerprint": fp, "changed": q.Changed, "previous": q.Previous})
		if err != nil || (d != "once" && d != "save") {
			return false, fmt.Errorf("certificate not trusted for %s:%d (%s)", host, port, q.Fingerprint)
		}
		return d == "save", nil
	}
	save := func(fp string) {
		if st, err := openStore(); err == nil {
			_ = st.SaveCertTrust(host, port, fp)
			st.Close()
		}
	}
	return pinned, ask, save
}

// askTrust shows the trust dialog ("ssh:trust" event) and waits for "once", "save" or "reject".
func (a *App) askTrust(payload map[string]any) (string, error) {
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
		payload["reqId"] = reqID
		a.emit("ssh:trust", payload)
	}
	select {
	case d := <-ch:
		return d, nil
	case <-time.After(2 * time.Minute):
		return "", errors.New("trust prompt timed out")
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
