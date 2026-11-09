package drdynvc

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

type fakeChannel struct {
	opened bool
	msgs   [][]byte
	closed bool
	write  func([]byte) error
}

func (f *fakeChannel) Open(w func([]byte) error) error { f.opened, f.write = true, w; return nil }
func (f *fakeChannel) Data(m []byte) error {
	f.msgs = append(f.msgs, append([]byte(nil), m...))
	return nil
}
func (f *fakeChannel) Close() { f.closed = true }

// chunk wraps a DVC PDU in one static channel chunk.
func chunk(pdu []byte) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(len(pdu)))
	b = binary.LittleEndian.AppendUint32(b, chanFirst|chanLast)
	return append(b, pdu...)
}

func setup(t *testing.T) (*Client, *fakeChannel, *[][]byte) {
	t.Helper()
	var sent [][]byte
	ch := &fakeChannel{}
	c := &Client{
		Send: func(b []byte) error { sent = append(sent, append([]byte(nil), b[8:]...)); return nil },
		Accept: func(name string) Channel {
			if name == "Microsoft::Windows::RDS::Graphics" {
				return ch
			}
			return nil
		},
	}
	return c, ch, &sent
}

func TestCapsCreateDataClose(t *testing.T) {
	c, ch, sent := setup(t)
	if err := c.Receive(chunk([]byte{0x50, 0x00, 0x03, 0x00})); err != nil { // caps v3
		t.Fatal(err)
	}
	if !bytes.Equal((*sent)[0], []byte{0x50, 0, 2, 0}) {
		t.Fatalf("caps reply %x, want version 2", (*sent)[0])
	}
	// create with a 2-byte id 0x0203 for the graphics channel, then a refused one
	if err := c.Receive(chunk(append([]byte{0x11, 0x03, 0x02}, "Microsoft::Windows::RDS::Graphics\x00"...))); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal((*sent)[1], []byte{0x11, 0x03, 0x02, 0, 0, 0, 0}) || !ch.opened {
		t.Fatalf("create response %x opened=%v", (*sent)[1], ch.opened)
	}
	if err := c.Receive(chunk(append([]byte{0x10, 0x07}, "Other\x00"...))); err != nil {
		t.Fatal(err)
	}
	if got := (*sent)[2]; !bytes.Equal(got[:2], []byte{0x10, 0x07}) || binary.LittleEndian.Uint32(got[2:]) == 0 {
		t.Fatalf("refusal %x", got)
	}
	// DATA_FIRST (4-byte length 6) + DATA, then a single DATA
	if err := c.Receive(chunk([]byte{0x29, 0x03, 0x02, 6, 0, 0, 0, 'a', 'b', 'c'})); err != nil {
		t.Fatal(err)
	}
	if len(ch.msgs) != 0 {
		t.Fatal("message delivered before it was complete")
	}
	if err := c.Receive(chunk([]byte{0x31, 0x03, 0x02, 'd', 'e', 'f'})); err != nil {
		t.Fatal(err)
	}
	if err := c.Receive(chunk([]byte{0x31, 0x03, 0x02, 'x'})); err != nil {
		t.Fatal(err)
	}
	if len(ch.msgs) != 2 || string(ch.msgs[0]) != "abcdef" || string(ch.msgs[1]) != "x" {
		t.Fatalf("messages %q", ch.msgs)
	}
	// data on the refused channel is dropped; too much data after DATA_FIRST is an error
	if err := c.Receive(chunk([]byte{0x30, 0x07, 'z'})); err != nil {
		t.Fatal(err)
	}
	if err := c.Receive(chunk([]byte{0x21, 0x03, 0x02, 2, 'a'})); err != nil {
		t.Fatal(err)
	}
	if err := c.Receive(chunk([]byte{0x31, 0x03, 0x02, 'b', 'c'})); err == nil {
		t.Fatal("overlong reassembly accepted")
	}
	if err := c.Receive(chunk([]byte{0x41, 0x03, 0x02})); err != nil || !ch.closed {
		t.Fatalf("close: %v closed=%v", err, ch.closed)
	}
	if got := (*sent)[len(*sent)-1]; !bytes.Equal(got, []byte{0x41, 0x03, 0x02}) {
		t.Fatalf("close reply %x", got)
	}
}

func TestStaticReassemblyAndCompressedRefused(t *testing.T) {
	c, _, sent := setup(t)
	pdu := []byte{0x50, 0x00, 0x01, 0x00}
	first := binary.LittleEndian.AppendUint32(nil, 4)
	first = binary.LittleEndian.AppendUint32(first, chanFirst)
	last := binary.LittleEndian.AppendUint32(nil, 4)
	last = binary.LittleEndian.AppendUint32(last, chanLast)
	if err := c.Receive(append(first, pdu[:2]...)); err != nil || len(*sent) != 0 {
		t.Fatalf("first chunk: %v %d", err, len(*sent))
	}
	if err := c.Receive(append(last, pdu[2:]...)); err != nil || len(*sent) != 1 {
		t.Fatalf("last chunk: %v %d", err, len(*sent))
	}
	if err := c.Receive(append(last, 1)); err == nil {
		t.Fatal("chunk without a first chunk accepted")
	}
	if err := c.Receive(chunk([]byte{0x60, 0x01})); err == nil {
		t.Fatal("compressed DVC data accepted")
	}
	big := binary.LittleEndian.AppendUint32(nil, maxStaticMessage+1)
	big = binary.LittleEndian.AppendUint32(big, chanFirst)
	if err := c.Receive(big); err == nil {
		t.Fatal("oversized static message accepted")
	}
}

func TestWriteSplitsLargeMessages(t *testing.T) {
	c, ch, sent := setup(t)
	_ = c.Receive(chunk(append([]byte{0x10, 0x05}, "Microsoft::Windows::RDS::Graphics\x00"...)))
	*sent = nil
	msg := bytes.Repeat([]byte{7}, 4000)
	if err := ch.write(msg); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for i, p := range *sent {
		if len(p) > MaxPDU {
			t.Fatalf("PDU %d of %d bytes", i, len(p))
		}
		if i == 0 {
			if p[0] != 0x28 || binary.LittleEndian.Uint32(p[2:]) != 4000 {
				t.Fatalf("DATA_FIRST header %x", p[:6])
			}
			got = append(got, p[6:]...)
		} else {
			if p[0] != 0x30 || p[1] != 5 {
				t.Fatalf("DATA header %x", p[:2])
			}
			got = append(got, p[2:]...)
		}
	}
	if !bytes.Equal(got, msg) {
		t.Fatal("split message does not reassemble")
	}
	*sent = nil
	_ = ch.write([]byte{1, 2})
	if !bytes.Equal((*sent)[0], []byte{0x30, 5, 1, 2}) {
		t.Fatalf("small DATA %x", (*sent)[0])
	}
}

func TestMalformedInputNeverPanics(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		c, _, _ := setup(t)
		_ = c.Receive(chunk(append([]byte{0x50, 0, 2, 0}, 0)[:4]))
		_ = c.Receive(chunk(append([]byte{0x10, 0x01}, "Microsoft::Windows::RDS::Graphics\x00"...)))
		b := make([]byte, rng.Intn(24))
		rng.Read(b)
		if rng.Intn(2) == 0 {
			_ = c.Receive(chunk(b))
		} else {
			_ = c.Receive(b)
		}
	}
}
