// rustxterm addition: tests for the changes made to the vendored client.

package rdp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"rustxterm-go/internal/rdpmit/protocol/pdu"
)

// TestStandardSecurityRefused: a server that selects standard RDP security (PROTOCOL_RDP) is refused
// right after the X.224 negotiation, so the client never sends its logon info unencrypted.
func TestStandardSecurityRefused(t *testing.T) {
	cli, srv := net.Pipe()
	dial := func(context.Context, string, string) (net.Conn, error) { return cli, nil }
	c, err := NewClientWithDialContext(context.Background(), dial, "lab:3389", "alice", "lab-alice-pass", 800, 600, 16)
	if err != nil {
		t.Fatal(err)
	}
	c.SetUseNLA(true)
	after := make(chan int, 1)
	go func() {
		buf := make([]byte, 1024)
		_, _ = srv.Read(buf) // X.224 Connection Request
		// Connection Confirm with RDP_NEG_RSP selecting PROTOCOL_RDP (0).
		_, _ = srv.Write([]byte{3, 0, 0, 19, 14, 0xd0, 0, 0, 0x12, 0x34, 0, 2, 0, 8, 0, 0, 0, 0, 0})
		n, _ := io.ReadFull(srv, buf[:1]) // anything else the client sends (it must send nothing)
		after <- n
	}()
	if err := c.Connect(); !errors.Is(err, ErrStandardSecurity) {
		t.Fatalf("Connect = %v, want ErrStandardSecurity", err)
	}
	_ = c.Close()
	if n := <-after; n != 0 {
		t.Fatal("the client kept talking after refusing standard security")
	}
}

func TestAddStaticChannel(t *testing.T) {
	c := &Client{}
	c.AddStaticChannel("cliprdr", 0xc0200000, func([]byte) {})
	c.AddStaticChannel("cliprdr", 0xc0200000, func([]byte) {}) // twice: requested once
	if len(c.channels) != 1 || c.channels[0] != "cliprdr" || c.channelOptions["cliprdr"] != 0xc0200000 {
		t.Fatalf("channels %v options %v", c.channels, c.channelOptions)
	}
	if err := c.SendStaticChannel("cliprdr", nil); err == nil {
		t.Fatal("sending on a channel that was not joined must fail")
	}
}

func TestNewLicenseRequestPDU(t *testing.T) {
	p := newLicenseRequestPDU("alice", "rustxterm", make([]byte, 32))
	// TS_SECURITY_HEADER (SEC_LICENSE_PKT) + LICENSE_PREAMBLE (NEW_LICENSE_REQUEST, v3, size)
	if p[0] != 0x80 || p[1] != 0 || p[4] != newLicenseRequest || p[5] != preambleVersion3 {
		t.Fatalf("header % x", p[:8])
	}
	if size := int(p[6]) | int(p[7])<<8; size != len(p)-4 {
		t.Fatalf("wMsgSize %d, PDU after the security header is %d bytes", size, len(p)-4)
	}
	if !bytes.HasSuffix(p, append([]byte{0x10, 0, 10, 0}, "rustxterm\x00"...)) {
		t.Fatalf("machine name blob missing: % x", p[len(p)-16:])
	}
}

func TestInputEventHeader(t *testing.T) {
	// FASTPATH_INPUT_EVENT_SCANCODE (0) with FASTPATH_INPUT_KBDFLAGS_RELEASE, scancode 0x1e.
	if b := pdu.NewKeyboardEvent(pdu.KBDFlagsRelease, 0x1e).Serialize(); !bytes.Equal(b, []byte{0x01, 0x1e}) {
		t.Fatalf("key release = % x", b)
	}
	// FASTPATH_INPUT_EVENT_MOUSE (1) is 0x20 in the event header.
	if b := pdu.NewMouseEvent(pdu.PTRFlagsMove, 1, 2).Serialize(); b[0] != 0x20 {
		t.Fatalf("mouse header = %#x", b[0])
	}
}
