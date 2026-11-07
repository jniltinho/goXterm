package gui

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"rustxterm-go/internal/fsentry"
	"rustxterm-go/internal/ftpconn"
	"rustxterm-go/internal/sftpconn"
	"rustxterm-go/internal/sshconn"
	"rustxterm-go/internal/store"
)

// remoteFS is the file-browser surface shared by SFTP and FTP/FTPS.
type remoteFS interface {
	Home() (string, error)
	List(dir string) ([]fsentry.Entry, error)
	Download(remote, local string) (int64, error)
	Upload(local, remote string) (int64, error)
	Close() error
}

// SftpSession is returned on connect: the tab id and the remote home directory to list first.
type SftpSession struct {
	TabID string `json:"tabId"`
	Home  string `json:"home"`
}

// SftpConnect opens an SFTP session for a saved SSH session.
func (a *App) SftpConnect(sessionID int64) (*SftpSession, error) {
	m, password, err := sessionRawByID(sessionID)
	if err != nil {
		return nil, err
	}
	host, port, user := asString(m["host"]), int(asInt64(m["port"])), asString(m["user"])
	if port == 0 {
		port = 22
	}
	sshOpt := subMap(m, "ssh")
	ask, saver := a.askSaving(host, user, sessionID)
	c, err := sftpconn.Connect(sshconn.Config{
		Host: host, Port: port, User: user, Password: password,
		KeyFile: asString(sshOpt["key_file"]), UseAgent: asBool(sshOpt["use_agent"]),
		AskPassword: ask, HostKey: a.sshHostKeyCallback(host, port),
	})
	if err != nil {
		return nil, err
	}
	_ = saver.commit() // a failed save only means the next login asks again
	return a.registerRemote(c, "s")
}

// FtpConnect opens an FTP/FTPS session (mode "plain"/"explicit"/"implicit") for a saved session.
func (a *App) FtpConnect(sessionID int64, mode string) (*SftpSession, error) {
	sv, password, err := a.sessionForFiles(sessionID)
	if err != nil {
		return nil, err
	}
	port := sv.Port
	if port == 0 {
		port = 21
	}
	// No saved password: ask (with "Save password") and keep it only after the login works.
	var saver *passwordSaver
	if password == "" {
		saver = &passwordSaver{sessionID: sessionID}
		pw, err := a.ask("", sv.Host, sv.User, saver)()
		if err != nil {
			return nil, err
		}
		password = pw
	}
	c, err := ftpconn.Connect(ftpconn.Config{
		Host: sv.Host, Port: port, User: sv.User, Password: password,
		Mode: ftpMode(mode), TLSInsecure: os.Getenv("RXT_SSH_INSECURE") == "1",
		VerifyTLS: a.ftpsVerifier(sv.Host, port),
	})
	if err != nil {
		return nil, err
	}
	if saver != nil {
		_ = saver.commit()
	}
	return a.registerRemote(c, "f")
}

// HasSftpDemo / ConnectSftpDemo auto-open an SFTP pane to RXT_DEMO_SSH (lab/demo only).
func (a *App) HasSftpDemo() bool { return os.Getenv("RXT_DEMO_SSH") != "" }

func (a *App) ConnectSftpDemo() (*SftpSession, error) {
	v := os.Getenv("RXT_DEMO_SSH")
	if v == "" {
		return nil, errors.New("no demo target")
	}
	user, pass, host, port := parseTarget(v)
	c, err := sftpconn.Connect(sshconn.Config{Host: host, Port: port, User: user, Password: pass, HostKey: a.sshHostKeyCallback(host, port)})
	if err != nil {
		return nil, err
	}
	return a.registerRemote(c, "s")
}

// HasFtpDemo / ConnectFtpDemo auto-open an FTP(S) pane to RXT_DEMO_FTP = "[plain|explicit|implicit ]user:pass@host:port".
func (a *App) HasFtpDemo() bool { return os.Getenv("RXT_DEMO_FTP") != "" }

func (a *App) ConnectFtpDemo() (*SftpSession, error) {
	v := os.Getenv("RXT_DEMO_FTP")
	if v == "" {
		return nil, errors.New("no demo target")
	}
	mode := ftpconn.Explicit
	if sp := strings.IndexByte(v, ' '); sp >= 0 {
		mode = ftpMode(v[:sp])
		v = strings.TrimSpace(v[sp+1:])
	}
	user, pass, host, port := parseTarget(v)
	if port == 0 {
		port = 21
	}
	c, err := ftpconn.Connect(ftpconn.Config{
		Host: host, Port: port, User: user, Password: pass, Mode: mode,
		TLSInsecure: os.Getenv("RXT_SSH_INSECURE") == "1",
	})
	if err != nil {
		return nil, err
	}
	return a.registerRemote(c, "f")
}

func ftpMode(s string) ftpconn.Mode {
	switch s {
	case "plain":
		return ftpconn.Plain
	case "implicit":
		return ftpconn.Implicit
	default:
		return ftpconn.Explicit
	}
}

// sessionForFiles loads a saved SSH/SFTP/FTP session and its saved password.
func (a *App) sessionForFiles(sessionID int64) (*SessionView, string, error) {
	st, err := openStore()
	if err != nil {
		return nil, "", err
	}
	defer st.Close()
	rows, err := st.Sessions()
	if err != nil {
		return nil, "", err
	}
	for _, m := range rows {
		if asInt64(m["id"]) == sessionID {
			s := viewOf(m)
			password := ""
			if secret, ok, _ := st.Credential(sessionID, store.CredPassword); ok {
				password = string(secret)
			}
			return &s, password, nil
		}
	}
	return nil, "", fmt.Errorf("session %d not found", sessionID)
}

func (a *App) registerRemote(c remoteFS, prefix string) (*SftpSession, error) {
	home, err := c.Home()
	if err != nil || home == "" {
		home = "."
	}
	a.mu.Lock()
	a.seq++
	tabID := prefix + strconv.Itoa(a.seq)
	if a.remote == nil {
		a.remote = map[string]remoteFS{}
	}
	a.remote[tabID] = c
	a.mu.Unlock()
	return &SftpSession{TabID: tabID, Home: home}, nil
}

// RemoteList lists a remote directory (SFTP or FTP).
func (a *App) RemoteList(tabID, dir string) ([]fsentry.Entry, error) {
	c := a.remoteClient(tabID)
	if c == nil {
		return nil, errors.New("the file session has closed")
	}
	return c.List(dir)
}

// RemoteDownload copies a remote file to localDir, returning the bytes written.
func (a *App) RemoteDownload(tabID, remote, localDir string) (int64, error) {
	c := a.remoteClient(tabID)
	if c == nil {
		return 0, errors.New("the file session has closed")
	}
	return c.Download(remote, filepath.Join(localDir, filepath.Base(remote)))
}

// RemoteUpload copies a local file to remoteDir, returning the bytes written.
func (a *App) RemoteUpload(tabID, local, remoteDir string) (int64, error) {
	c := a.remoteClient(tabID)
	if c == nil {
		return 0, errors.New("the file session has closed")
	}
	remote := strings.TrimRight(remoteDir, "/") + "/" + filepath.Base(local)
	if !a.asciiOn(tabID) {
		return c.Upload(local, remote)
	}
	b, err := os.ReadFile(local)
	if err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp("", "rxt-ascii-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(asciiText(b, false))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, err
	}
	return c.Upload(tmp.Name(), remote)
}

// RemoteSetASCII turns the SFTP panel's "ASCII mode" on or off for a file session: uploads send LF
// line ends, downloads get the local platform's (CR LF on Windows).
func (a *App) RemoteSetASCII(tabID string, on bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ascii == nil {
		a.ascii = map[string]bool{}
	}
	if on {
		a.ascii[tabID] = true
	} else {
		delete(a.ascii, tabID)
	}
}

func (a *App) asciiOn(tabID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ascii[tabID]
}

// asciiText converts text line ends to LF, or to CR LF when crlf is set (ASCII transfer mode).
func asciiText(b []byte, crlf bool) []byte {
	lf := bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	if !crlf {
		return lf
	}
	return bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))
}

// RemoteDisconnect closes a remote file session.
func (a *App) RemoteDisconnect(tabID string) error {
	a.mu.Lock()
	c := a.remote[tabID]
	delete(a.remote, tabID)
	a.mu.Unlock()
	if c == nil {
		return nil
	}
	return c.Close()
}

func (a *App) remoteClient(tabID string) remoteFS {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.remote[tabID]
}

// ── local filesystem side of the dual pane

// LocalHome is the user's home directory (the local pane starts here).
func (a *App) LocalHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "/"
	}
	return h
}

// LocalList lists a local directory, directories first then by name.
func (a *App) LocalList(dir string) ([]fsentry.Entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]fsentry.Entry, 0, len(des))
	for _, de := range des {
		fi, err := de.Info()
		if err != nil {
			continue
		}
		out = append(out, fsentry.Entry{
			Name: fi.Name(), Dir: fi.IsDir(), Size: fi.Size(), Link: fi.Mode()&os.ModeSymlink != 0,
			Mode: fsentry.LsMode(fi.Mode()), ModTime: fi.ModTime().Unix(),
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

// RemoteNewFile creates an empty file (the SFTP panel's "Create new file"), over SFTP or FTP alike.
func (a *App) RemoteNewFile(tabID, remote string) error {
	c := a.remoteClient(tabID)
	if c == nil {
		return errors.New("the file session has closed")
	}
	f, err := os.CreateTemp("", "rxt-empty-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	defer os.Remove(name)
	_, err = c.Upload(name, remote)
	return err
}

// ftpsVerifier is the FTPS certificate trust (tlstrust), off only for the lab override.
func (a *App) ftpsVerifier(host string, port int) func(tls.ConnectionState) error {
	if os.Getenv("RXT_SSH_INSECURE") == "1" {
		return nil
	}
	return a.tlsVerifier(host, port)
}
