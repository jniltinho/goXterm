// Package localconn runs a local shell in a pseudo-terminal (the "+" tab), streaming bytes like the
// SSH terminal so the same xterm.js view renders it. Uses creack/pty (openpty on Linux/macOS, ConPTY
// on Windows) — Go-native, cross-platform (migrate-gui-to-go-wails — local shell, core separation).
package localconn

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"

	"github.com/creack/pty"
)

// Session is a running local shell on a PTY.
type Session struct {
	ptmx   *os.File
	cmd    *exec.Cmd
	mu     sync.Mutex
	closed bool
}

// DefaultShell is the shell Start runs when none is given (shown in the local terminal's banner).
func DefaultShell() string { return defaultShell() }

// defaultShell picks the user's shell, falling back per OS.
func defaultShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	if runtime.GOOS == "windows" {
		if cs := os.Getenv("COMSPEC"); cs != "" {
			return cs
		}
		return "powershell.exe"
	}
	return "/bin/sh"
}

// Start launches the shell in a PTY and streams its output to onData until it exits, then calls
// onClose once. onData gets owned copies of the bytes.
func Start(shell string, cols, rows int, onData func([]byte), onClose func(error)) (*Session, error) {
	if shell == "" {
		shell = defaultShell()
	}
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	cmd := exec.Command(shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	s := &Session{ptmx: ptmx, cmd: cmd}
	go s.read(onData, onClose)
	return s, nil
}

func (s *Session) read(onData func([]byte), onClose func(error)) {
	buf := make([]byte, 4096)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 && onData != nil {
			onData(append([]byte(nil), buf[:n]...))
		}
		if err != nil {
			s.markClosed()
			_ = s.cmd.Wait()
			if onClose != nil {
				var reason error // "exited with status N", shown in the stopped notice (Rust parity)
				if ps := s.cmd.ProcessState; ps != nil {
					reason = fmt.Errorf("exited with status %d", ps.ExitCode())
				}
				onClose(reason)
			}
			return
		}
	}
}

// Write sends user input to the shell.
func (s *Session) Write(p []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return os.ErrClosed
	}
	_, err := s.ptmx.Write(p)
	return err
}

// Resize changes the PTY window size.
func (s *Session) Resize(cols, rows int) error {
	return pty.Setsize(s.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// Close ends the shell and releases the PTY.
func (s *Session) Close() error {
	s.markClosed()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return s.ptmx.Close()
}

func (s *Session) markClosed() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}
