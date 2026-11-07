// Package sshconn is a minimal SSH terminal client (pure Go, golang.org/x/crypto/ssh): connect,
// request a PTY, start a shell, and stream bytes both ways. Host-key verification is the caller's
// (migrate-gui-to-go-wails phase 5 — SSH + terminal).
package sshconn

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Config describes one SSH connection.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string // static password/keyboard-interactive auth, tried first when non-empty
	// AskPassword, when set, is called to prompt the user for a password (or a key passphrase) when
	// one is needed. It may block (e.g. a UI dialog); the answer is reused within this dial.
	AskPassword func() (string, error)
	KeyFile     string // optional private key path (public-key auth); ~ is expanded
	UseAgent    bool   // also try the ssh-agent at $SSH_AUTH_SOCK
	HostKey     ssh.HostKeyCallback
	Cols, Rows  int
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
	// ki builds a keyboard-interactive method that answers every prompt with getPw() (PAM / network
	// gear ask the password this way).
	ki := func(getPw func() (string, error)) ssh.AuthMethod {
		return ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
			if len(questions) == 0 {
				return nil, nil
			}
			pw, err := getPw()
			if err != nil {
				return nil, err
			}
			ans := make([]string, len(questions))
			for i := range ans {
				ans[i] = pw
			}
			return ans, nil
		})
	}
	// Public-key auth (ssh-agent and/or an explicit key file) is tried before passwords.
	if cfg.UseAgent {
		if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
			if ac, err := net.Dial("unix", sock); err == nil {
				auths = append(auths, ssh.PublicKeysCallback(agent.NewClient(ac).Signers))
			}
		}
	}
	if cfg.KeyFile != "" {
		// Lazily: the key is loaded only if the agent did not already authenticate, so a
		// passphrase-protected key never prompts when the agent holds it (fixes "asks the password
		// even with a key"). A load error skips the method and falls through to the password.
		auths = append(auths, ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
			signer, err := loadKey(cfg.KeyFile, cfg.AskPassword)
			if err != nil {
				return nil, nil
			}
			return []ssh.Signer{signer}, nil
		}))
	}
	if cfg.Password != "" {
		pw := cfg.Password
		auths = append(auths, ssh.Password(pw), ki(func() (string, error) { return pw, nil }))
	}
	if cfg.AskPassword != nil {
		// Prompt once, then reuse the answer across the remaining method attempts in this dial.
		var cached string
		var have bool
		ask := func() (string, error) {
			if have {
				return cached, nil
			}
			pw, err := cfg.AskPassword()
			if err != nil {
				return "", err
			}
			cached, have = pw, true
			return pw, nil
		}
		auths = append(auths, ssh.PasswordCallback(ask), ki(ask))
	}
	clientCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auths,
		HostKeyCallback: cfg.HostKey,
		Timeout:         15 * time.Second,
	}
	return ssh.Dial("tcp", net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port)), clientCfg)
}

// loadKey reads a private key file and returns its signer. If the key is passphrase-protected and
// ask is set, it prompts for the passphrase once.
func loadKey(path string, ask func() (string, error)) (ssh.Signer, error) {
	b, err := os.ReadFile(expandHome(path))
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(b)
	if err == nil {
		return signer, nil
	}
	var pm *ssh.PassphraseMissingError
	if errors.As(err, &pm) && ask != nil {
		pw, aerr := ask()
		if aerr != nil {
			return nil, aerr
		}
		return ssh.ParsePrivateKeyWithPassphrase(b, []byte(pw))
	}
	return nil, err
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
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

// Run executes a command on a fresh exec channel of the same connection and returns its combined
// output. Used by the remote-monitoring collector alongside the shell.
func (s *Session) Run(cmd string) ([]byte, error) {
	s.mu.Lock()
	c, closed := s.client, s.closed
	s.mu.Unlock()
	if closed || c == nil {
		return nil, fmt.Errorf("session closed")
	}
	es, err := c.NewSession()
	if err != nil {
		return nil, err
	}
	defer es.Close()
	return es.CombinedOutput(cmd)
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
