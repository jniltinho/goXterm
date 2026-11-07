package rdp

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"

	"rustxterm-go/internal/rdpmit/logging"
)

// StartTLS upgrades the connection to TLS for RDP security.
// rustxterm: the global config/env lookup was removed; the settings come from SetTLSClientConfig,
// otherwise from SetTLSConfig (upstream defaults).
func (c *Client) StartTLS() error {
	return c.handshakeTLS(c.clientTLSConfig(tls.VersionTLS13))
}

// clientTLSConfig is the caller's TLS config (cloned), or upstream's defaults; maxVersion caps
// the version only when the caller did not choose one.
func (c *Client) clientTLSConfig(maxVersion uint16) *tls.Config {
	var cfg *tls.Config
	if c.tlsConfig != nil {
		cfg = c.tlsConfig.Clone()
	} else {
		cfg = &tls.Config{
			InsecureSkipVerify: c.skipTLSValidation, // #nosec G402 -- RDP servers commonly use self-signed certificates
			MinVersion:         tls.VersionTLS12,
			ServerName:         c.tlsServerName,
		}
		if c.skipTLSValidation {
			cfg.MinVersion = tls.VersionTLS10
		}
	}
	if cfg.MaxVersion == 0 {
		cfg.MaxVersion = maxVersion
	}
	if cfg.ServerName == "" {
		cfg.ServerName = c.getServerName()
	}
	if cfg.ServerName == "" && c.conn != nil {
		if host, _, err := net.SplitHostPort(c.conn.RemoteAddr().String()); err == nil {
			cfg.ServerName = host
		}
	}
	return cfg
}

// handshakeTLS runs the TLS handshake (certificate checks included) and swaps the connection.
func (c *Client) handshakeTLS(tlsConfig *tls.Config) error {
	tlsConn := tls.Client(c.conn, tlsConfig)

	// Set handshake timeout to prevent hanging
	_ = c.conn.SetDeadline(time.Now().Add(30 * time.Second))
	if err := tlsConn.Handshake(); err != nil {
		return fmt.Errorf("TLS handshake failed: %w", err)
	}
	_ = c.conn.SetDeadline(time.Time{})

	state := tlsConn.ConnectionState()
	logging.Info("TLS: version=%s cipher=%s serverName=%s",
		tlsVersionString(state.Version), tls.CipherSuiteName(state.CipherSuite), state.ServerName)

	c.conn = tlsConn
	c.buffReader = bufio.NewReaderSize(c.conn, readBufferSize)

	return nil
}

func (c *Client) getServerName() string {
	if c.conn == nil {
		return ""
	}

	remoteAddr := c.conn.RemoteAddr().String()
	if remoteAddr == "" {
		return ""
	}

	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// Fallback to extracting from hostname if available
		parts := strings.Split(remoteAddr, ":")
		if len(parts) > 0 {
			host = parts[0]
		} else {
			return ""
		}
	}

	// Clean up the hostname
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}

	// For IP addresses, we might not want to verify the hostname anyway
	// Return empty to let Go use the IP directly
	if net.ParseIP(host) != nil {
		return ""
	}

	// Validate hostname format
	if len(host) > 253 { // Maximum hostname length
		return ""
	}

	return host
}

func (c *Client) getMinTLSVersion(version string) uint16 {
	switch version {
	case "1.0":
		return tls.VersionTLS10
	case "1.1":
		return tls.VersionTLS11
	case "1.2":
		return tls.VersionTLS12
	case "1.3":
		return tls.VersionTLS13
	default:
		return tls.VersionTLS12 // Safe default
	}
}

func tlsVersionString(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "TLS1.0"
	case tls.VersionTLS11:
		return "TLS1.1"
	case tls.VersionTLS12:
		return "TLS1.2"
	case tls.VersionTLS13:
		return "TLS1.3"
	default:
		return fmt.Sprintf("0x%04x", version)
	}
}
