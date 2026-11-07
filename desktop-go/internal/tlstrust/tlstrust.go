// Package tlstrust decides whether a TLS server certificate is trusted (RDP, FTPS), as the Rust
// app's rdp/trust.rs and ftp/tls.rs: a certificate the system trusts for the host passes; otherwise
// the SHA-256 fingerprint pinned for host:port must match; otherwise the user is asked, and may pin
// it. It holds no UI and no store: the caller supplies the pin and the question.
package tlstrust

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
)

// Fingerprint is the upper-case hex SHA-256 of a DER certificate, "AA:BB:…" (Rust trust::fingerprint).
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// Question is what the user is asked about an untrusted certificate.
type Question struct {
	Fingerprint string
	Subject     string // the certificate's common name (or full subject)
	Changed     bool   // a different certificate was pinned for this host before
}

// Ask returns whether to continue and whether to pin the certificate; an error refuses it.
type Ask func(Question) (pin bool, err error)

// Verifier is a tls.Config.VerifyConnection callback (use with InsecureSkipVerify, which only
// skips Go's own check: this callback then decides). pinned is the stored fingerprint ("" = none);
// save stores a newly pinned one.
func Verifier(host, pinned string, ask Ask, save func(fingerprint string)) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("the server sent no certificate")
		}
		leaf := cs.PeerCertificates[0]
		inter := x509.NewCertPool()
		for _, c := range cs.PeerCertificates[1:] {
			inter.AddCert(c)
		}
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Intermediates: inter}); err == nil {
			return nil // a certificate the system trusts for this host
		}
		fp := Fingerprint(leaf.Raw)
		if pinned != "" && strings.EqualFold(pinned, fp) {
			return nil
		}
		subject := leaf.Subject.CommonName
		if subject == "" {
			subject = leaf.Subject.String()
		}
		pin, err := ask(Question{Fingerprint: fp, Subject: subject, Changed: pinned != ""})
		if err != nil {
			return err
		}
		if pin && save != nil {
			save(fp)
		}
		return nil
	}
}
