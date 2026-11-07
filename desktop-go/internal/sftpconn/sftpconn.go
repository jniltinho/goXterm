// Package sftpconn is an SFTP client over the shared SSH dialer (pkg/sftp): list, download, upload
// and basic directory operations (migrate-gui-to-go-wails phase 6 — SFTP / file transfer).
package sftpconn

import (
	"io"
	"os"
	"sort"

	"github.com/pkg/sftp"

	"rustxterm-go/internal/fsentry"
	"rustxterm-go/internal/sshconn"
)

// Client is an open SFTP connection (and its SSH transport).
type Client struct {
	ssh  interface{ Close() error }
	sftp *sftp.Client
}

// Connect opens an SFTP session to host:port as user.
func Connect(cfg sshconn.Config) (*Client, error) {
	client, err := sshconn.DialClient(cfg)
	if err != nil {
		return nil, err
	}
	sc, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		return nil, err
	}
	return &Client{ssh: client, sftp: sc}, nil
}

// Home returns the server's starting directory.
func (c *Client) Home() (string, error) { return c.sftp.Getwd() }

// List returns the entries of a remote directory, directories first then by name.
func (c *Client) List(dir string) ([]fsentry.Entry, error) {
	infos, err := c.sftp.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]fsentry.Entry, 0, len(infos))
	for _, fi := range infos {
		out = append(out, fsentry.Entry{
			Name: fi.Name(), Dir: fi.IsDir(), Size: fi.Size(),
			Mode: fi.Mode().String(), ModTime: fi.ModTime().Unix(),
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
	rf, err := c.sftp.Open(remote)
	if err != nil {
		return 0, err
	}
	defer rf.Close()
	lf, err := os.Create(local)
	if err != nil {
		return 0, err
	}
	defer lf.Close()
	return io.Copy(lf, rf)
}

// Upload copies a local file to a remote path, returning the byte count.
func (c *Client) Upload(local, remote string) (int64, error) {
	lf, err := os.Open(local)
	if err != nil {
		return 0, err
	}
	defer lf.Close()
	rf, err := c.sftp.Create(remote)
	if err != nil {
		return 0, err
	}
	defer rf.Close()
	return io.Copy(rf, lf)
}

// Mkdir, Remove and Rename are the basic directory operations.
func (c *Client) Mkdir(dir string) error       { return c.sftp.Mkdir(dir) }
func (c *Client) Remove(path string) error     { return c.sftp.Remove(path) }
func (c *Client) Rename(old, new string) error { return c.sftp.Rename(old, new) }

// Close ends the SFTP session and its SSH transport.
func (c *Client) Close() error {
	_ = c.sftp.Close()
	return c.ssh.Close()
}
