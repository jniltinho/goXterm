//go:build !windows

package localconn

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// unixPTY is a program on an openpty pseudo-terminal (creack/pty).
type unixPTY struct {
	*os.File // the master side
	cmd      *exec.Cmd
}

func startPTY(path string, args []string, dir string, cols, rows int) (ptyProc, error) {
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &unixPTY{File: f, cmd: cmd}, nil
}

func (u *unixPTY) Resize(cols, rows int) error {
	return pty.Setsize(u.File, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (u *unixPTY) Wait() (int, error) {
	_ = u.cmd.Wait()
	if ps := u.cmd.ProcessState; ps != nil {
		return ps.ExitCode(), nil
	}
	return 0, os.ErrProcessDone
}

func (u *unixPTY) Kill() error {
	if u.cmd.Process == nil {
		return nil
	}
	return u.cmd.Process.Kill()
}
