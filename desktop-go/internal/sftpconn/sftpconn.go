// Package sftpconn is an SFTP client over the shared SSH dialer (pkg/sftp): list, download, upload
// and basic directory operations (migrate-gui-to-go-wails phase 6 — SFTP / file transfer).
package sftpconn

import (
	"bufio"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"rustxterm-go/internal/fsentry"
	"rustxterm-go/internal/sshconn"
)

// Client is an open SFTP connection (and its SSH transport).
type Client struct {
	ssh  interface{ Close() error }
	sftp *sftp.Client

	idsOnce sync.Once
	users   map[uint32]string // uid → name, from the server's /etc/passwd
	groups  map[uint32]string // gid → name, from /etc/group
}

// readIDs maps the server's uids and gids to names from /etc/passwd and /etc/group (read once over
// SFTP; a server without them, e.g. Windows OpenSSH, shows the numbers).
func (c *Client) readIDs() {
	c.idsOnce.Do(func() {
		c.users, c.groups = c.idFile("/etc/passwd"), c.idFile("/etc/group")
	})
}

func (c *Client) idFile(p string) map[uint32]string {
	m := map[uint32]string{}
	f, err := c.sftp.Open(p)
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(io.LimitReader(f, 4<<20))
	for sc.Scan() {
		// name:x:id:… — the third field is the uid (passwd) or gid (group)
		parts := strings.SplitN(sc.Text(), ":", 4)
		if len(parts) < 3 {
			continue
		}
		if id, err := strconv.ParseUint(parts[2], 10, 32); err == nil {
			if _, dup := m[uint32(id)]; !dup {
				m[uint32(id)] = parts[0]
			}
		}
	}
	return m
}

func idName(names map[uint32]string, id uint32) string {
	if n, ok := names[id]; ok {
		return n
	}
	return strconv.FormatUint(uint64(id), 10)
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

// FromSSH opens an SFTP subsystem on an existing SSH connection (a terminal tab's), so the side
// browser needs no second login. Close ends only the SFTP channel; the connection stays the tab's.
func FromSSH(client *ssh.Client) (*Client, error) {
	sc, err := sftp.NewClient(client)
	if err != nil {
		return nil, err
	}
	return &Client{sftp: sc}, nil
}

// RealPath resolves a path on the server (symlinks included), e.g. /proc/<pid>/cwd.
func (c *Client) RealPath(p string) (string, error) { return c.sftp.RealPath(p) }

// Home returns the server's starting directory.
func (c *Client) Home() (string, error) { return c.sftp.Getwd() }

// List returns the entries of a remote directory, directories first then by name.
func (c *Client) List(dir string) ([]fsentry.Entry, error) {
	infos, err := c.sftp.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	c.readIDs()
	out := make([]fsentry.Entry, 0, len(infos))
	for _, fi := range infos {
		e := fsentry.Entry{
			Name: fi.Name(), Dir: fi.IsDir(), Size: fi.Size(), Link: fi.Mode()&os.ModeSymlink != 0,
			Mode: fsentry.LsMode(fi.Mode()), ModTime: fi.ModTime().Unix(),
		}
		if st, ok := fi.Sys().(*sftp.FileStat); ok {
			e.Owner, e.Group = idName(c.users, st.UID), idName(c.groups, st.GID)
		}
		out = append(out, e)
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

// Close ends the SFTP session and, when it owns one, its SSH transport.
func (c *Client) Close() error {
	err := c.sftp.Close()
	if c.ssh != nil {
		return c.ssh.Close()
	}
	return err
}
