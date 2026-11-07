package gui

import (
	"errors"
	"fmt"
	"os"

	"rustxterm-go/internal/telnetconn"
)

// TelnetConnect opens a Telnet terminal for a saved session and returns its tabId; output streams as
// "term:out:<tabId>" like SSH, so the same Terminal.vue renders it.
func (a *App) TelnetConnect(sessionID int64) (string, error) {
	sv, err := sessionByID(sessionID)
	if err != nil {
		return "", err
	}
	if sv.Protocol != "telnet" {
		return "", fmt.Errorf("session %d is not telnet", sessionID)
	}
	return a.telnetDial(sv.Host, sv.Port)
}

// TelnetQuickConnect opens a Telnet terminal to host:port (quick-connect bar and tests).
func (a *App) TelnetQuickConnect(host string, port int) (string, error) {
	return a.telnetDial(host, port)
}

// HasTelnetDemo / ConnectTelnetDemo auto-open a Telnet terminal to RXT_DEMO_TELNET = "host:port".
func (a *App) HasTelnetDemo() bool { return os.Getenv("RXT_DEMO_TELNET") != "" }

func (a *App) ConnectTelnetDemo() (string, error) {
	v := os.Getenv("RXT_DEMO_TELNET")
	if v == "" {
		return "", errors.New("no demo target")
	}
	host := v
	port := 23
	if c := lastIndexByte(v, ':'); c >= 0 {
		host = v[:c]
		if p := atoiOr(v[c+1:], 0); p > 0 {
			port = p
		}
	}
	return a.telnetDial(host, port)
}

func (a *App) telnetDial(host string, port int) (string, error) {
	tabID, onData, onClose := a.newStreamTab()
	sess, err := telnetconn.Connect(telnetconn.Config{Host: host, Port: port}, onData, onClose)
	if err != nil {
		a.dropEarly(tabID)
		return "", err
	}
	a.setBanner(tabID, a.telnetBanner(host, port))
	a.mu.Lock()
	a.term[tabID] = sess
	a.mu.Unlock()
	return tabID, nil
}
