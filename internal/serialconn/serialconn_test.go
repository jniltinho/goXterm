package serialconn

import (
	"testing"

	"go.bug.st/serial"
)

// TestModeMapping checks Config → serial.Mode: defaults (9600 8N1) and the parity/stop-bit/baud
// conversions, which is the only non-trivial logic here (read/write just wrap the library).
func TestModeMapping(t *testing.T) {
	def := mode(Config{})
	if def.BaudRate != 9600 || def.DataBits != 8 || def.Parity != serial.NoParity || def.StopBits != serial.OneStopBit {
		t.Fatalf("default = %+v, want 9600 8N1", def)
	}

	m := mode(Config{Baud: 115200, DataBits: 7, Parity: "even", StopBits: "2"})
	if m.BaudRate != 115200 || m.DataBits != 7 || m.Parity != serial.EvenParity || m.StopBits != serial.TwoStopBits {
		t.Fatalf("custom = %+v, want 115200 7E2", m)
	}

	if mode(Config{Parity: "odd"}).Parity != serial.OddParity {
		t.Fatal("odd parity not mapped")
	}
	if mode(Config{StopBits: "1.5"}).StopBits != serial.OnePointFiveStopBits {
		t.Fatal("1.5 stop bits not mapped")
	}
}
