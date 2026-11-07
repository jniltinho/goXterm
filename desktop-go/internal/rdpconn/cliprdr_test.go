package rdpconn

import (
	"encoding/binary"
	"strings"
	"testing"
)

// fakeServer collects the PDUs the client sends (reassembled from channel chunks).
type fakeServer struct{ pdus [][]byte }

func (f *fakeServer) recv() func([]byte) error {
	var buf []byte
	return func(b []byte) error {
		flags := binary.LittleEndian.Uint32(b[4:])
		if flags&chanFirst != 0 {
			buf = nil
		}
		buf = append(buf, b[8:]...)
		if flags&chanLast != 0 {
			f.pdus = append(f.pdus, buf)
		}
		return nil
	}
}

func (f *fakeServer) types() []uint16 {
	var t []uint16
	for _, p := range f.pdus {
		t = append(t, binary.LittleEndian.Uint16(p))
	}
	return t
}

// toClient frames a server PDU as channel chunks and feeds it to the client.
func toClient(c *Clip, typ, flags uint16, body []byte) {
	p := make([]byte, 8)
	binary.LittleEndian.PutUint16(p, typ)
	binary.LittleEndian.PutUint16(p[2:], flags)
	binary.LittleEndian.PutUint32(p[4:], uint32(len(body)))
	_ = chunks(append(p, body...), func(b []byte) error { c.Receive(b); return nil })
}

func TestHandshakeAndServerCopy(t *testing.T) {
	srv := &fakeServer{}
	var got string
	c := &Clip{Send: srv.recv(), OnText: func(s string) { got = s }}

	toClient(c, msgClipCaps, 0, clientCaps()) // server also supports long names
	toClient(c, msgMonitorReady, 0, nil)
	if ty := srv.types(); len(ty) != 2 || ty[0] != msgClipCaps || ty[1] != msgFormatList {
		t.Fatalf("after Monitor Ready the client sends caps then a format list, got %v", ty)
	}

	// The server copies text: a long-name format list with CF_UNICODETEXT.
	toClient(c, msgFormatList, 0, []byte{cfUnicodeText, 0, 0, 0, 0, 0})
	ty := srv.types()
	if ty[2] != msgFormatListRsp || ty[3] != msgDataRequest {
		t.Fatalf("want list response then data request, got %v", ty)
	}
	if f := binary.LittleEndian.Uint32(srv.pdus[3][8:]); f != cfUnicodeText {
		t.Fatalf("requested format %d", f)
	}
	toClient(c, msgDataResponse, respOK, utf16z("olá RDP"))
	if got != "olá RDP" {
		t.Fatalf("server text = %q", got)
	}
}

func TestLocalTextIsOfferedAndSent(t *testing.T) {
	srv := &fakeServer{}
	c := &Clip{Send: srv.recv()}
	toClient(c, msgMonitorReady, 0, nil) // short names (no server caps)
	long := strings.Repeat("x", 2000)    // spans two channel chunks
	if err := c.SetText(long); err != nil {
		t.Fatal(err)
	}
	last := srv.pdus[len(srv.pdus)-1]
	if binary.LittleEndian.Uint16(last) != msgFormatList || len(last) != 8+36 || last[8] != cfUnicodeText {
		t.Fatalf("short-name format list not offered: % x", last[:12])
	}
	req := make([]byte, 4)
	binary.LittleEndian.PutUint32(req, cfUnicodeText)
	toClient(c, msgDataRequest, 0, req)
	rsp := srv.pdus[len(srv.pdus)-1]
	if binary.LittleEndian.Uint16(rsp) != msgDataResponse || fromUTF16z(rsp[8:]) != long {
		t.Fatal("data response does not carry the local text")
	}
}

func TestParseFormats(t *testing.T) {
	long := []byte{1, 0, 0, 0, 'A', 0, 0, 0, 13, 0, 0, 0, 0, 0}
	if f := parseFormats(long, true); len(f) != 2 || f[0] != 1 || f[1] != 13 {
		t.Fatalf("long names: %v", f)
	}
	if f := parseFormats(make([]byte, 72), false); len(f) != 2 {
		t.Fatalf("short names: %v", f)
	}
}
