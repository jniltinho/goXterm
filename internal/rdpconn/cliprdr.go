// The client side of the RDP clipboard virtual channel (MS-RDPECLIP), text only: the server's copied
// text is delivered to OnText, and the local text set with SetText is offered to the server and sent
// when it pastes. Static virtual channel framing (MS-RDPBCGR 2.2.6.1) included. Written for this
// project (not derived from grdp).

package rdpconn

import (
	"bytes"
	"encoding/binary"
	"sync"
	"unicode/utf16"
)

// clipChannel is the static virtual channel this plugin speaks on.
const clipChannel = "cliprdr"

// Channel definition options for the client network data (MS-RDPBCGR 2.2.1.3.4.1).
const clipOptions = 0x80000000 | 0x40000000 | 0x00200000 // INITIALIZED | ENCRYPT_RDP | SHOW_PROTOCOL

const (
	chanFirst        = 0x01
	chanLast         = 0x02
	chanShowProtocol = 0x10
	chunkSize        = 1600

	msgMonitorReady  = 0x0001
	msgFormatList    = 0x0002
	msgFormatListRsp = 0x0003
	msgDataRequest   = 0x0004
	msgDataResponse  = 0x0005
	msgClipCaps      = 0x0007

	respOK   = 0x0001
	respFail = 0x0002

	capsGeneral   = 0x0001
	capsVersion2  = 0x00000002
	useLongNames  = 0x00000002
	cfText        = 1
	cfUnicodeText = 13
)

// Clip is one session's clipboard channel. Send writes one channel chunk (header included) to the
// "cliprdr" MCS channel; OnText receives text the remote side copied.
type Clip struct {
	Send   func([]byte) error
	OnText func(string)

	mu        sync.Mutex
	local     string
	ready     bool // the server sent Monitor Ready: format lists may flow
	longNames bool // both sides support long format names
	asked     uint32
	in        bytes.Buffer
}

// SetText offers local clipboard text to the server (a Format List with CF_UNICODETEXT).
func (c *Clip) SetText(s string) error {
	c.mu.Lock()
	changed := s != c.local
	c.local = s
	ready := c.ready
	c.mu.Unlock()
	if !ready || !changed {
		return nil
	}
	return c.sendFormatList()
}

// Receive handles one virtual channel chunk as delivered by the security layer.
func (c *Clip) Receive(b []byte) {
	if len(b) < 8 {
		return
	}
	flags := binary.LittleEndian.Uint32(b[4:8])
	c.mu.Lock()
	if flags&chanFirst != 0 {
		c.in.Reset()
	}
	c.in.Write(b[8:])
	if flags&chanLast == 0 {
		c.mu.Unlock()
		return
	}
	pdu := append([]byte(nil), c.in.Bytes()...)
	c.in.Reset()
	c.mu.Unlock()
	c.handle(pdu)
}

func (c *Clip) handle(p []byte) {
	if len(p) < 8 {
		return
	}
	typ, flags := binary.LittleEndian.Uint16(p), binary.LittleEndian.Uint16(p[2:])
	n := int(binary.LittleEndian.Uint32(p[4:]))
	body := p[8:]
	if n < len(body) {
		body = body[:n]
	}
	switch typ {
	case msgClipCaps:
		c.mu.Lock()
		c.longNames = serverLongNames(body)
		c.mu.Unlock()
	case msgMonitorReady:
		_ = c.send(msgClipCaps, 0, clientCaps())
		c.mu.Lock()
		c.ready = true
		c.mu.Unlock()
		_ = c.sendFormatList()
	case msgFormatList:
		_ = c.send(msgFormatListRsp, respOK, nil)
		c.mu.Lock()
		formats := parseFormats(body, c.longNames)
		c.mu.Unlock()
		want := uint32(0)
		for _, f := range formats {
			if f == cfUnicodeText {
				want = f
				break
			}
			if f == cfText {
				want = f
			}
		}
		if want != 0 {
			c.mu.Lock()
			c.asked = want
			c.mu.Unlock()
			req := make([]byte, 4)
			binary.LittleEndian.PutUint32(req, want)
			_ = c.send(msgDataRequest, 0, req)
		}
	case msgDataRequest:
		if len(body) < 4 {
			return
		}
		format := binary.LittleEndian.Uint32(body)
		c.mu.Lock()
		text := c.local
		c.mu.Unlock()
		switch format {
		case cfUnicodeText:
			_ = c.send(msgDataResponse, respOK, utf16z(text))
		case cfText:
			_ = c.send(msgDataResponse, respOK, append([]byte(text), 0))
		default:
			_ = c.send(msgDataResponse, respFail, nil)
		}
	case msgDataResponse:
		c.mu.Lock()
		format := c.asked
		c.asked = 0
		c.mu.Unlock()
		if flags&respOK == 0 || c.OnText == nil {
			return
		}
		text := string(bytes.TrimRight(body, "\x00"))
		if format == cfUnicodeText {
			text = fromUTF16z(body)
		}
		c.mu.Lock()
		c.local = text // it comes back from the local clipboard on focus: do not offer it again
		c.mu.Unlock()
		c.OnText(text)
	}
}

func serverLongNames(body []byte) bool {
	if len(body) < 4 {
		return false
	}
	count := int(binary.LittleEndian.Uint16(body))
	r := body[4:]
	for i := 0; i < count && len(r) >= 4; i++ {
		typ, l := binary.LittleEndian.Uint16(r), int(binary.LittleEndian.Uint16(r[2:]))
		if l < 4 || l > len(r) {
			return false
		}
		if typ == capsGeneral && l >= 12 {
			return binary.LittleEndian.Uint32(r[8:])&useLongNames != 0
		}
		r = r[l:]
	}
	return false
}

func clientCaps() []byte {
	b := make([]byte, 16)
	binary.LittleEndian.PutUint16(b, 1)               // cCapabilitiesSets
	binary.LittleEndian.PutUint16(b[4:], capsGeneral) // capabilitySetType
	binary.LittleEndian.PutUint16(b[6:], 12)          // lengthCapability
	binary.LittleEndian.PutUint32(b[8:], capsVersion2)
	binary.LittleEndian.PutUint32(b[12:], useLongNames)
	return b
}

// parseFormats returns the format ids of a Format List body (long or short names).
func parseFormats(b []byte, long bool) []uint32 {
	var out []uint32
	if !long {
		for ; len(b) >= 36; b = b[36:] {
			out = append(out, binary.LittleEndian.Uint32(b))
		}
		return out
	}
	for len(b) >= 4 {
		out = append(out, binary.LittleEndian.Uint32(b))
		b = b[4:]
		for len(b) >= 2 { // UTF-16 name up to and including its NUL
			z := b[0] == 0 && b[1] == 0
			b = b[2:]
			if z {
				break
			}
		}
	}
	return out
}

func (c *Clip) sendFormatList() error {
	c.mu.Lock()
	has, long := c.local != "", c.longNames
	c.mu.Unlock()
	var body []byte
	if has {
		if long {
			body = []byte{cfUnicodeText, 0, 0, 0, 0, 0} // id + empty UTF-16 name
		} else {
			body = make([]byte, 36)
			body[0] = cfUnicodeText
		}
	}
	return c.send(msgFormatList, 0, body)
}

// send frames one clipboard PDU into virtual channel chunks.
func (c *Clip) send(typ, flags uint16, body []byte) error {
	if c.Send == nil {
		return nil
	}
	p := make([]byte, 8, 8+len(body))
	binary.LittleEndian.PutUint16(p, typ)
	binary.LittleEndian.PutUint16(p[2:], flags)
	binary.LittleEndian.PutUint32(p[4:], uint32(len(body)))
	p = append(p, body...)
	return chunks(p, c.Send)
}

func chunks(p []byte, send func([]byte) error) error {
	for off := 0; ; {
		end := min(off+chunkSize, len(p))
		flags := uint32(chanShowProtocol)
		if off == 0 {
			flags |= chanFirst
		}
		if end == len(p) {
			flags |= chanLast
		}
		h := make([]byte, 8, 8+end-off)
		binary.LittleEndian.PutUint32(h, uint32(len(p)))
		binary.LittleEndian.PutUint32(h[4:], flags)
		if err := send(append(h, p[off:end]...)); err != nil {
			return err
		}
		if off = end; off >= len(p) {
			return nil
		}
	}
}

func utf16z(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u)+2)
	for i, v := range u {
		binary.LittleEndian.PutUint16(b[2*i:], v)
	}
	return b
}

func fromUTF16z(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		v := binary.LittleEndian.Uint16(b[i:])
		if v == 0 {
			break
		}
		u = append(u, v)
	}
	return string(utf16.Decode(u))
}
