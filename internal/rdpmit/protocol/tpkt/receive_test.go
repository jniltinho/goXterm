package tpkt

import (
	"bytes"
	"io"
	"testing"
)

// trickle returns at most one byte per Read, like a buffered connection with little data in it.
type trickle struct{ r io.Reader }

func (t trickle) Read(p []byte) (int, error) { return t.r.Read(p[:min(len(p), 1)]) }
func (trickle) Write(p []byte) (int, error)  { return len(p), nil }
func (trickle) Close() error                 { return nil }

func TestReceiveReadsWholePackets(t *testing.T) {
	payload := bytes.Repeat([]byte{0xAB}, 1700)
	pkt := append([]byte{3, 0, byte((len(payload) + 4) >> 8), byte(len(payload) + 4)}, payload...)
	p := New(trickle{bytes.NewReader(append(pkt, 3, 0, 0, 2))})
	wire, err := p.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(wire); !bytes.Equal(got, payload) {
		t.Fatalf("payload of %d bytes, want %d intact", len(got), len(payload))
	}
	if _, err := p.Receive(); err == nil {
		t.Fatal("length below the header accepted")
	}
}
