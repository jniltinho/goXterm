// Package sshconn is a minimal SSH terminal client (pure Go, golang.org/x/crypto/ssh): connect,
// request a PTY, start a shell, and stream bytes both ways. Host-key verification is the caller's
// (migrate-gui-to-go-wails phase 5 — SSH + terminal).
package sshconn

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Config describes one SSH connection.
type Config struct {
	Host       string
	Port       int
	User       string
	Password   string // password and keyboard-interactive auth; key/agent come later
	HostKey    ssh.HostKeyCallback
	Cols, Rows int
}

// Session is an open SSH shell with a PTY.
type Session struct {
	client *ssh.Client
	sess   *ssh.Session
	stdin  io.WriteCloser
	mu     sync.Mutex
	closed bool
}

// DialClient authenticates and returns an SSH client (password + keyboard-interactive auth; key and
// agent come later). Shared by the terminal and SFTP.
func DialClient(cfg Config) (*ssh.Client, error) {
	var auths []ssh.AuthMethod
	if cfg.Password != "" {
		pw := cfg.Password
		auths = append(auths, ssh.Password(pw))
		// PAM / network gear ask the password as a keyboard-interactive prompt.
		auths = append(auths, ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
			ans := make([]string, len(questions))
			for i := range ans {
				ans[i] = pw
			}
			return ans, nil
		}))
	}
	clientCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auths,
		HostKeyCallback: cfg.HostKey,
		Timeout:         15 * time.Second,
	}
	return ssh.Dial("tcp", net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port)), clientCfg)
}

// Connect dials, authenticates, opens a PTY shell, and pumps output to onData until it ends, then
// calls onClose once. onData receives owned copies of the bytes.
func Connect(cfg Config, onData func([]byte), onClose func(error)) (*Session, error) {
	client, err := DialClient(cfg)
	if err != nil {
		return nil, err
	}
	sess, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, err
	}
	cols, rows := cfg.Cols, cfg.Rows
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		sess.Close()
		client.Close()
		return nil, err
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		client.Close()
		return nil, err
	}
	stdout, _ := sess.StdoutPipe()
	stderr, _ := sess.StderrPipe()
	if err := sess.Shell(); err != nil {
		sess.Close()
		client.Close()
		return nil, err
	}
	s := &Session{client: client, sess: sess, stdin: stdin}

	pump := func(r io.Reader) {
		buf := make([]byte, 32*1024)
		for {
			n, err := r.Read(buf)
			if n > 0 && onData != nil {
				onData(append([]byte(nil), buf[:n]...))
			}
			if err != nil {
				return
			}
		}
	}
	go pump(stdout)
	go pump(stderr)
	go func() {
		err := s.sess.Wait()
		s.markClosed()
		if onClose != nil {
			onClose(err)
		}
	}()
	return s, nil
}

// Write sends input to the shell.
func (s *Session) Write(p []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("session closed")
	}
	_, err := s.stdin.Write(p)
	return err
}

// Resize changes the remote terminal size.
func (s *Session) Resize(cols, rows int) error {
	return s.sess.WindowChange(rows, cols)
}

// Close ends the session.
func (s *Session) Close() error {
	s.markClosed()
	_ = s.sess.Close()
	return s.client.Close()
}

func (s *Session) markClosed() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}
