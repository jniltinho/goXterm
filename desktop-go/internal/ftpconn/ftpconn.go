// Package ftpconn is an FTP/FTPS client (jlaffaye/ftp) with the same file-browser surface as sftpconn:
// list, download, upload. Supports plain FTP, explicit FTPS (AUTH TLS) and implicit FTPS
// (migrate-gui-to-go-wails phase 7 — FTP / FTPS).
package ftpconn

import (
	"crypto/tls"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/jlaffaye/ftp"

	"rustxterm-go/internal/fsentry"
)

// Mode selects the transport: plain FTP, explicit FTPS (AUTH TLS on 21/2121) or implicit FTPS (990/2990).
type Mode string

const (
	Plain    Mode = "plain"
	Explicit Mode = "explicit"
	Implicit Mode = "implicit"
)

// Config describes one FTP(S) connection.
type Config struct {
	Host        string
	Port        int
	User        string
	Password    string
	Mode        Mode
	TLSInsecure bool // accept a self-signed server certificate (lab/tests)
	// VerifyTLS decides whether the server certificate is trusted (tlstrust.Verifier).
	VerifyTLS func(tls.ConnectionState) error
}

// Client is an open FTP(S) connection.
type Client struct{ conn *ftp.ServerConn }

// Connect dials and logs in.
func Connect(cfg Config) (*Client, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	opts := []ftp.DialOption{ftp.DialWithTimeout(15 * time.Second)}
	if cfg.Mode == Explicit || cfg.Mode == Implicit {
		tc := &tls.Config{ServerName: cfg.Host, InsecureSkipVerify: cfg.TLSInsecure || cfg.VerifyTLS != nil,
			VerifyConnection: cfg.VerifyTLS}
		if cfg.Mode == Implicit {
			opts = append(opts, ftp.DialWithTLS(tc))
		} else {
			opts = append(opts, ftp.DialWithExplicitTLS(tc))
		}
	}
	c, err := ftp.Dial(addr, opts...)
	if err != nil {
		return nil, err
	}
	if err := c.Login(cfg.User, cfg.Password); err != nil {
		_ = c.Quit()
		return nil, err
	}
	return &Client{conn: c}, nil
}

// Home returns the current remote directory.
func (c *Client) Home() (string, error) { return c.conn.CurrentDir() }

// List returns the entries of a remote directory, directories first then by name.
func (c *Client) List(dir string) ([]fsentry.Entry, error) {
	es, err := c.conn.List(dir)
	if err != nil {
		return nil, err
	}
	out := make([]fsentry.Entry, 0, len(es))
	for _, e := range es {
		if e.Name == "." || e.Name == ".." {
			continue
		}
		out = append(out, fsentry.Entry{
			Name: e.Name, Dir: e.Type == ftp.EntryTypeFolder, Link: e.Type == ftp.EntryTypeLink,
			Size: int64(e.Size), ModTime: e.Time.Unix(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Download copies a remote file to a local path, returning the byte count.
func (c *Client) Download(remote, local string) (int64, error) {
	r, err := c.conn.Retr(remote)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	f, err := os.Create(local)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, r)
}

// Upload copies a local file to a remote path, returning the byte count.
func (c *Client) Upload(local, remote string) (int64, error) {
	f, err := os.Open(local)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if err := c.conn.Stor(remote, f); err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// Close logs out and closes the connection.
func (c *Client) Close() error { return c.conn.Quit() }
