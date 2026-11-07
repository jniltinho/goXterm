// Package telnetconn is a minimal Telnet client (RFC 854) on the standard library: it dials TCP,
// answers IAC option negotiation in line mode, and streams the remaining bytes to a callback so the
// same xterm.js terminal renders it (migrate-gui-to-go-wails — Telnet, Go-native, core separation).
// Login is interactive, through the stream, like a real telnet; there is no separate auth step.
package telnetconn

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// Telnet command and option bytes (RFC 854/858/857/1184).
const (
	iac  = 255
	dont = 254
	do   = 253
	wont = 252
	will = 251
	sb   = 250
	se   = 240

	optEcho = 1 // server echo
	optSGA  = 3 // suppress go ahead (char-at-a-time)
)

// Config describes one Telnet connection.
type Config struct {
	Host string
	Port int
}

// Session is an open Telnet connection.
type Session struct {
	conn   net.Conn
	mu     sync.Mutex
	closed bool
}

// Connect dials the server and starts streaming. onData gets decoded output (IAC sequences removed),
// onClose fires once when the connection ends.
func Connect(cfg Config, onData func([]byte), onClose func(error)) (*Session, error) {
	port := cfg.Port
	if port == 0 {
		port = 23
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", cfg.Host, port), 15*time.Second)
	if err != nil {
		return nil, err
	}
	s := &Session{conn: conn}
	go s.read(onData, onClose)
	return s, nil
}

// read parses the Telnet stream: IAC command sequences are answered in line mode (we let the server
// echo and suppress go-ahead, and refuse everything else), and the payload bytes go to onData.
func (s *Session) read(onData func([]byte), onClose func(error)) {
	buf := make([]byte, 4096)
	out := make([]byte, 0, 4096)
	for {
		n, err := s.conn.Read(buf)
		if n > 0 {
			out = out[:0]
			for i := 0; i < n; i++ {
				if buf[i] != iac {
					out = append(out, buf[i])
					continue
				}
				// IAC: need the command byte (and option); read more if the frame is split.
				cmd, ok := s.at(buf, n, i+1, onData, &out)
				if !ok {
					return
				}
				switch cmd {
				case iac: // escaped literal 0xFF
					out = append(out, iac)
					i++
				case will, wont, do, dont:
					opt, ok := s.at(buf, n, i+2, onData, &out)
					if !ok {
						return
					}
					s.respond(cmd, opt)
					i += 2
				case sb: // skip subnegotiation up to IAC SE
					i += 2
					for i < n && !(buf[i] == iac && i+1 < n && buf[i+1] == se) {
						i++
					}
					i++ // land on SE
				default:
					i++ // 2-byte command without option
				}
			}
			if len(out) > 0 && onData != nil {
				onData(append([]byte(nil), out...))
			}
		}
		if err != nil {
			s.markClosed()
			if onClose != nil {
				onClose(nil)
			}
			return
		}
	}
}

// at returns the byte at position p in the current buffer, flushing pending output and blocking for
// the next TCP segment when an IAC frame straddles a read boundary.
func (s *Session) at(buf []byte, n, p int, onData func([]byte), out *[]byte) (byte, bool) {
	if p < n {
		return buf[p], true
	}
	if len(*out) > 0 && onData != nil {
		onData(append([]byte(nil), *out...))
		*out = (*out)[:0]
	}
	one := make([]byte, 1)
	if _, err := s.conn.Read(one); err != nil {
		return 0, false
	}
	return one[0], true
}

// respond answers one option negotiation in line mode: accept server ECHO and SGA (so the remote
// side echoes and we get char-at-a-time), refuse to enable any option on our side.
func (s *Session) respond(cmd, opt byte) {
	var reply byte
	switch cmd {
	case will:
		if opt == optEcho || opt == optSGA {
			reply = do
		} else {
			reply = dont
		}
	case do:
		reply = wont
	default: // wont / dont: nothing to confirm
		return
	}
	s.mu.Lock()
	if !s.closed {
		s.conn.Write([]byte{iac, reply, opt})
	}
	s.mu.Unlock()
}

// Write sends user input to the server (IAC bytes are escaped).
func (s *Session) Write(p []byte) error {
	esc := p
	for _, b := range p {
		if b == iac { // only allocate when an escape is actually needed
			esc = escapeIAC(p)
			break
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("the telnet connection has closed")
	}
	_, err := s.conn.Write(esc)
	return err
}

func escapeIAC(p []byte) []byte {
	out := make([]byte, 0, len(p)+4)
	for _, b := range p {
		out = append(out, b)
		if b == iac {
			out = append(out, iac)
		}
	}
	return out
}

// Resize is a no-op: NAWS window-size negotiation is a follow-up; line-mode telnet ignores size.
func (s *Session) Resize(cols, rows int) error { return nil }

// Close ends the session.
func (s *Session) Close() error {
	s.markClosed()
	return s.conn.Close()
}

func (s *Session) markClosed() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}
