package rdpconn

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"
)

// TestLegacyTLSRSAKeyExchange: a TLS 1.0 server offering only RSA key exchange (Windows Server 2008 R2's RDP
// listener) is reached in legacy mode, and refused by the default TLS 1.2+ settings.
func TestLegacyTLSRSAKeyExchange(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "WIN2008R2"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	server := &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS10, MaxVersion: tls.VersionTLS10,
		CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_128_CBC_SHA},
	}
	handshake := func(legacy bool) error {
		c, s := net.Pipe()
		defer c.Close()
		defer s.Close()
		go func() { _ = tls.Server(s, server).Handshake() }()
		return tls.Client(c, tlsConfig(Config{Host: "192.0.2.1", LegacyTLS: legacy})).Handshake()
	}
	if err := handshake(true); err != nil {
		t.Fatalf("legacy TLS: %v", err)
	}
	if err := handshake(false); err == nil {
		t.Fatal("default TLS settings accepted TLS 1.0")
	}
}
