package gui

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"rustxterm-go/internal/sftpconn"
	"rustxterm-go/internal/sshconn"
)

// Side SFTP browser of an SSH terminal tab and "Follow terminal folder" — a port of the Rust app's
// engine/follow.rs. The browser is an SFTP channel on the tab's own connection (no second login),
// registered as the tab's remote file session so RemoteList/Download/Upload work on it.
//
// Following without shell cooperation: one exec on the same connection runs psCommand. The exec's
// own process ($$) and the tab's login shell are both children of the server's per-connection
// session process, so the login shell is the process with the same parent that owns a terminal (tty
// not ?); the youngest wins. Each Enter in the tab then asks TermCwd, which resolves
// /proc/<pid>/cwd over SFTP. OSC 7 from the terminal stream is handled in the SPA first.

const psCommand = "exec sh -c 'echo $$; ps -eo tty,pid,ppid,etime,args'"

const (
	shellNotYet      = 0
	shellUnsupported = -1
)

// findShell returns the login shell PID, shellNotYet, or shellUnsupported (no usable ps -eo table).
func findShell(output string) int {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	me, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return shellUnsupported
	}
	type row struct {
		tty       string
		pid, ppid int
		age       int64
	}
	var rows []row
	for _, l := range lines[1:] {
		f := strings.Fields(l)
		if len(f) < 4 {
			continue
		}
		pid, e1 := strconv.Atoi(f[1])
		ppid, e2 := strconv.Atoi(f[2])
		age, ok := elapsedSecs(f[3])
		if e1 != nil || e2 != nil || !ok {
			continue // the header and unparsable lines
		}
		rows = append(rows, row{f[0], pid, ppid, age})
	}
	parent := -1
	for _, r := range rows {
		if r.pid == me {
			parent = r.ppid
		}
	}
	if parent < 0 {
		return shellUnsupported
	}
	best, bestAge := shellNotYet, int64(-1)
	for _, r := range rows {
		if r.ppid == parent && r.pid != me && r.tty != "?" && r.tty != "-" && (bestAge < 0 || r.age < bestAge) {
			best, bestAge = r.pid, r.age
		}
	}
	return best
}

// elapsedSecs parses ps etime "[[dd-]hh:]mm:ss".
func elapsedSecs(s string) (int64, bool) {
	var days int64
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.ParseInt(d, 10, 64)
		if err != nil {
			return 0, false
		}
		days, s = n, rest
	}
	var secs int64
	for _, part := range strings.Split(s, ":") {
		n, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return 0, false
		}
		secs = secs*60 + n
	}
	return days*86400 + secs, true
}

func (a *App) sshTab(tabID string) (*sshconn.Session, error) {
	s, ok := a.session(tabID).(*sshconn.Session)
	if !ok || s == nil {
		return nil, errors.New("the SFTP browser needs an open SSH tab")
	}
	return s, nil
}

// TermFiles opens (or reuses) the side SFTP browser on an SSH terminal tab's connection and returns
// its starting directory. The browser uses the terminal's tabId for RemoteList etc.
func (a *App) TermFiles(tabID string) (*SftpSession, error) {
	if c, ok := a.remoteClient(tabID).(*sftpconn.Client); ok {
		home, _ := c.Home()
		return &SftpSession{TabID: tabID, Home: home}, nil
	}
	s, err := a.sshTab(tabID)
	if err != nil {
		return nil, err
	}
	c, err := sftpconn.FromSSH(s.SSHClient())
	if err != nil {
		return nil, fmt.Errorf("SFTP not offered by the server: %w", err)
	}
	a.mu.Lock()
	if a.remote == nil {
		a.remote = map[string]remoteFS{}
	}
	a.remote[tabID] = c
	a.mu.Unlock()
	home, _ := c.Home()
	return &SftpSession{TabID: tabID, Home: home}, nil
}

// TermCwd returns the current folder of an SSH tab's login shell (Follow terminal folder).
func (a *App) TermCwd(tabID string) (string, error) {
	s, err := a.sshTab(tabID)
	if err != nil {
		return "", err
	}
	c, ok := a.remoteClient(tabID).(*sftpconn.Client)
	if !ok {
		return "", errors.New("open the SFTP browser first")
	}
	for attempt := 0; attempt < 2; attempt++ {
		pid := a.shellPIDFor(tabID, s, attempt > 0)
		if pid <= 0 {
			return "", errors.New("following the terminal folder is not supported on this server")
		}
		if p, err := c.RealPath(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil {
			return p, nil
		}
		// The shell is gone or /proc is missing: detect once more.
	}
	return "", errors.New("cannot read the terminal folder")
}

// shellPIDFor detects (and caches) the tab's login shell, retrying ~5 s while it is not listed yet.
func (a *App) shellPIDFor(tabID string, s *sshconn.Session, refresh bool) int {
	a.mu.Lock()
	pid, known := a.shellPID[tabID]
	a.mu.Unlock()
	if known && !refresh {
		return pid
	}
	pid = shellUnsupported
	for try := 0; try < 5; try++ {
		out, err := s.Run(psCommand)
		if err != nil && len(out) == 0 {
			break
		}
		if pid = findShell(string(out)); pid != shellNotYet {
			break
		}
		time.Sleep(time.Second)
	}
	if pid == shellNotYet {
		pid = shellUnsupported
	}
	a.mu.Lock()
	if a.shellPID == nil {
		a.shellPID = map[string]int{}
	}
	a.shellPID[tabID] = pid
	a.mu.Unlock()
	return pid
}

// remoteEditor is the optional part of a remote file session (SFTP has it, FTP does not yet).
type remoteEditor interface {
	Mkdir(dir string) error
	Remove(path string) error
	Rename(oldPath, newPath string) error
}

func (a *App) editor(tabID string) (remoteEditor, error) {
	e, ok := a.remoteClient(tabID).(remoteEditor)
	if !ok {
		return nil, errors.New("not supported on this file session")
	}
	return e, nil
}

// RemoteMkdir / RemoteRemove / RemoteRename edit the remote side of a file session.
func (a *App) RemoteMkdir(tabID, dir string) error {
	e, err := a.editor(tabID)
	if err != nil {
		return err
	}
	return e.Mkdir(dir)
}

func (a *App) RemoteRemove(tabID, p string) error {
	e, err := a.editor(tabID)
	if err != nil {
		return err
	}
	return e.Remove(p)
}

func (a *App) RemoteRename(tabID, oldPath, newPath string) error {
	e, err := a.editor(tabID)
	if err != nil {
		return err
	}
	return e.Rename(oldPath, newPath)
}

// RemoteDownloadAs downloads a remote file to a place chosen in a native save dialog; it returns the
// local path ("" when cancelled).
func (a *App) RemoteDownloadAs(tabID, remote string) (string, error) {
	c := a.remoteClient(tabID)
	if c == nil {
		return "", errors.New("the file session has closed")
	}
	if a.savePath == nil {
		return "", errors.New("no save dialog")
	}
	local, err := a.savePath(path.Base(remote))
	if err != nil || local == "" {
		return "", err
	}
	_, err = c.Download(remote, local)
	return local, err
}

// RemoteUploadPick uploads a file chosen in a native open dialog into remoteDir; it returns the
// uploaded file's name ("" when cancelled).
func (a *App) RemoteUploadPick(tabID, remoteDir string) (string, error) {
	if a.openPath == nil {
		return "", errors.New("no open dialog")
	}
	local, err := a.openPath("Upload a file")
	if err != nil || local == "" {
		return "", err
	}
	if _, err := a.RemoteUpload(tabID, local, remoteDir); err != nil {
		return "", err
	}
	return local, nil
}
