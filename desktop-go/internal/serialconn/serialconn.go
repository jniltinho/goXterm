// Package serialconn is a serial-port client on go.bug.st/serial (pure Go, Linux/macOS/Windows): it
// opens a port at a given baud/framing and streams bytes to a callback so the same xterm.js terminal
// renders it (migrate-gui-to-go-wails — Serial, Go-native, core separation).
package serialconn

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

// Config describes one serial connection. Device is the port path (/dev/ttyUSB0, COM3). Unset fields
// default to 9600 8N1, the common console framing.
type Config struct {
	Device   string
	Baud     int
	DataBits int
	Parity   string // "none" | "even" | "odd"
	StopBits string // "1" | "1.5" | "2"
}

// Session is an open serial port.
type Session struct {
	port   serial.Port
	mu     sync.Mutex
	closed bool
}

// Ports lists the serial ports available on this machine (for the session editor's device picker).
func Ports() ([]string, error) { return serial.GetPortsList() }

// Connect opens the port and starts streaming. onData gets bytes as they arrive; onClose fires once
// when the read loop ends.
func Connect(cfg Config, onData func([]byte), onClose func(error)) (*Session, error) {
	if cfg.Device == "" {
		return nil, fmt.Errorf("serial: no device")
	}
	port, err := serial.Open(cfg.Device, mode(cfg))
	if err != nil {
		return nil, err
	}
	s := &Session{port: port}
	go s.read(onData, onClose)
	return s, nil
}

func mode(cfg Config) *serial.Mode {
	m := &serial.Mode{BaudRate: 9600, DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit}
	if cfg.Baud > 0 {
		m.BaudRate = cfg.Baud
	}
	if cfg.DataBits > 0 {
		m.DataBits = cfg.DataBits
	}
	switch strings.ToLower(cfg.Parity) {
	case "even":
		m.Parity = serial.EvenParity
	case "odd":
		m.Parity = serial.OddParity
	}
	switch cfg.StopBits {
	case "2":
		m.StopBits = serial.TwoStopBits
	case "1.5":
		m.StopBits = serial.OnePointFiveStopBits
	}
	return m
}

func (s *Session) read(onData func([]byte), onClose func(error)) {
	buf := make([]byte, 4096)
	for {
		n, err := s.port.Read(buf)
		if n > 0 && onData != nil {
			onData(append([]byte(nil), buf[:n]...))
		}
		if err != nil || n == 0 { // n==0 on a closed port with go.bug.st/serial
			s.markClosed()
			if onClose != nil {
				onClose(nil)
			}
			return
		}
	}
}

// Write sends user input to the port.
func (s *Session) Write(p []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("the serial port has closed")
	}
	_, err := s.port.Write(p)
	return err
}

// SendBreak holds a break condition on the line for ms milliseconds (agent serial_send_break).
func (s *Session) SendBreak(ms uint64) error {
	if err := s.open(); err != nil {
		return err
	}
	return s.port.Break(time.Duration(ms) * time.Millisecond)
}

// SetLines sets DTR and/or RTS; a nil value leaves that line as it is (agent serial_set_lines).
func (s *Session) SetLines(dtr, rts *bool) error {
	if err := s.open(); err != nil {
		return err
	}
	if dtr != nil {
		if err := s.port.SetDTR(*dtr); err != nil {
			return err
		}
	}
	if rts != nil {
		return s.port.SetRTS(*rts)
	}
	return nil
}

func (s *Session) open() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("the serial port has closed")
	}
	return nil
}

// Resize is a no-op: a serial console has no window size.
func (s *Session) Resize(cols, rows int) error { return nil }

// Close ends the session.
func (s *Session) Close() error {
	s.markClosed()
	return s.port.Close()
}

func (s *Session) markClosed() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}
