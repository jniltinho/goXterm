// Package localconn runs a local shell in a pseudo-terminal (the "+" tab), streaming bytes like the
// SSH terminal so the same xterm.js view renders it. The pseudo-terminal is creack/pty's openpty on
// Linux/macOS and a native ConPTY (golang.org/x/sys/windows) on Windows — Go-native, cross-platform.
// StartCommand also runs other console programs in it (wsl.exe for WSL sessions).
package localconn

import (
	"fmt"
	"os"
	"runtime"
	"sync"
)

// ptyProc is a program running in a pseudo-terminal (pty_unix.go, pty_windows.go).
type ptyProc interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows int) error
	Wait() (exitCode int, err error) // after the output ended
	Kill() error
	Close() error
}

// Session is a running program on a PTY.
type Session struct {
	p      ptyProc
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
	return StartCommand(shell, nil, "", cols, rows, onData, onClose)
}

// StartCommand runs path with args (in dir when set) on a PTY, like Start.
func StartCommand(path string, args []string, dir string, cols, rows int, onData func([]byte), onClose func(error)) (*Session, error) {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	p, err := startPTY(path, args, dir, cols, rows)
	if err != nil {
		return nil, err
	}
	s := &Session{p: p}
	go s.read(onData, onClose)
	return s, nil
}

func (s *Session) read(onData func([]byte), onClose func(error)) {
	buf := make([]byte, 4096)
	for {
		n, err := s.p.Read(buf)
		if n > 0 && onData != nil {
			onData(append([]byte(nil), buf[:n]...))
		}
		if err != nil {
			s.markClosed()
			code, werr := s.p.Wait()
			if onClose != nil {
				var reason error // "exited with status N", shown in the stopped notice (Rust parity)
				if werr == nil {
					reason = fmt.Errorf("exited with status %d", code)
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
	_, err := s.p.Write(p)
	return err
}

// Resize changes the PTY window size.
func (s *Session) Resize(cols, rows int) error {
	return s.p.Resize(cols, rows)
}

// Close ends the shell and releases the PTY.
func (s *Session) Close() error {
	s.markClosed()
	_ = s.p.Kill()
	return s.p.Close()
}

func (s *Session) markClosed() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}
