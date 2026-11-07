package gui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"rustxterm-go/internal/serialconn"
)

// SerialPorts lists the serial ports on this machine (for the session editor's device picker).
func (a *App) SerialPorts() ([]string, error) { return serialconn.Ports() }

// SerialConnect opens a serial terminal for a saved session, reading the device and line config
// (baud/data bits/parity/stop bits) from the stored serial options. Output streams like SSH.
func (a *App) SerialConnect(sessionID int64) (string, error) {
	m, _, err := sessionRawByID(sessionID)
	if err != nil {
		return "", err
	}
	if asString(m["protocol"]) != "serial" {
		return "", fmt.Errorf("session %d is not serial", sessionID)
	}
	ser := subMap(m, "serial")
	line := subMap(ser, "line")
	return a.serialDial(serialconn.Config{
		Device:   asString(ser["port"]),
		Baud:     int(asInt64(line["baud"])),
		DataBits: atoiOr(asString(line["data_bits"]), 0),
		Parity:   asString(line["parity"]),
		StopBits: asString(line["stop_bits"]),
	})
}

// SerialOpen opens a serial terminal for an explicit device/baud (session editor and tests).
func (a *App) SerialOpen(device string, baud int) (string, error) {
	return a.serialDial(serialconn.Config{Device: device, Baud: baud})
}

// HasSerialDemo / ConnectSerialDemo auto-open a serial terminal to RXT_DEMO_SERIAL = "device[:baud]".
func (a *App) HasSerialDemo() bool { return os.Getenv("RXT_DEMO_SERIAL") != "" }

func (a *App) ConnectSerialDemo() (string, error) {
	v := os.Getenv("RXT_DEMO_SERIAL")
	if v == "" {
		return "", errors.New("no demo target")
	}
	device, baud := v, 0
	if c := strings.LastIndexByte(v, ':'); c >= 0 {
		device, baud = v[:c], atoiOr(v[c+1:], 0)
	}
	return a.serialDial(serialconn.Config{Device: device, Baud: baud})
}

func (a *App) serialDial(cfg serialconn.Config) (string, error) {
	tabID, onData, onClose := a.newStreamTab()
	sess, err := serialconn.Connect(cfg, onData, onClose)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.term[tabID] = sess
	a.mu.Unlock()
	return tabID, nil
}
