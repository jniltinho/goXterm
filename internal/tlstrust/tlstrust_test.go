package tlstrust

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"
)

func selfSigned(t *testing.T) *x509.Certificate {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "lab-rdp"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"rdp"}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c
}

func TestFingerprintFormat(t *testing.T) {
	fp := Fingerprint(nil) // same vector as the Rust test: SHA-256 of nothing
	if !strings.HasPrefix(fp, "E3:B0:C4:42") || len(fp) != 32*3-1 {
		t.Fatal(fp)
	}
}

func TestVerifierPinsAndAsks(t *testing.T) {
	c := selfSigned(t)
	cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{c}}
	fp := Fingerprint(c.Raw)
	refuse := func(Question) (bool, error) { return false, errors.New("refused") }

	if err := Verifier("rdp", fp, refuse, nil)(cs); err != nil {
		t.Fatalf("pinned certificate refused: %v", err)
	}
	var asked Question
	var saved string
	err := Verifier("rdp", "", func(q Question) (bool, error) { asked = q; return true, nil }, func(f string) { saved = f })(cs)
	if err != nil || saved != fp || asked.Subject != "lab-rdp" || asked.Changed {
		t.Fatalf("first use: err=%v saved=%q asked=%+v", err, saved, asked)
	}
	if err := Verifier("rdp", "00:11", func(q Question) (bool, error) { asked = q; return false, errors.New("no") }, nil)(cs); err == nil || !asked.Changed || asked.Previous != "00:11" {
		t.Fatalf("changed certificate not reported: %v %+v", err, asked)
	}
	if err := Verifier("rdp", "", refuse, nil)(tls.ConnectionState{}); err == nil {
		t.Fatal("no certificate accepted")
	}
}

// TestLegacyVerifier: as the Rust app — unknown key asks (and pins on "save"), the pinned key
// passes, a changed key refuses, and a host pinned over TLS refuses (downgrade).
func TestLegacyVerifier(t *testing.T) {
	fp := LegacyKeyPrefix + "AA:BB"
	var asked Question
	saved := ""
	v := LegacyVerifier("h:3389", "", func(q Question) (bool, error) { asked = q; return true, nil }, func(s string) { saved = s })
	if err := v(fp); err != nil || saved != fp || asked.Subject != LegacySubject || asked.Fingerprint != fp {
		t.Fatalf("first use: %v saved %q asked %+v", err, saved, asked)
	}
	never := func(Question) (bool, error) { t.Fatal("asked"); return false, nil }
	if err := LegacyVerifier("h:3389", fp, never, nil)(fp); err != nil {
		t.Fatalf("pinned: %v", err)
	}
	if err := LegacyVerifier("h:3389", LegacyKeyPrefix+"CC:DD", never, nil)(fp); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed: %v", err)
	}
	if err := LegacyVerifier("h:3389", "AA:BB:CC", never, nil)(fp); !errors.Is(err, ErrDowngrade) {
		t.Fatalf("pinned over TLS: %v", err)
	}
	if err := LegacyVerifier("h:3389", "", func(Question) (bool, error) { return false, errors.New("rejected") }, nil)(fp); err == nil {
		t.Fatal("a rejected key was accepted")
	}
}
