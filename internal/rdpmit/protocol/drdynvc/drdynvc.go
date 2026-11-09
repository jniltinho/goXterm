// rustxterm addition (not in upstream go-rdp; its drdynvc package was not vendored because its ZGFX
// is not the MS-RDPEGFX algorithm): the client side of the Dynamic Virtual Channel protocol
// (MS-RDPEDYC) carried by the "drdynvc" static virtual channel, written from the specification.

// Package drdynvc runs dynamic virtual channels over the drdynvc static channel: capabilities,
// channel creation and close, and DATA_FIRST/DATA reassembly, with bounded buffers.
package drdynvc

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ChannelName is the static virtual channel that carries the dynamic channels.
const ChannelName = "drdynvc"

// Options are the CHANNEL_DEF options for the static channel: INITIALIZED | ENCRYPT_RDP.
const Options = 0x80000000 | 0x40000000

const (
	cmdCreate              = 0x01
	cmdDataFirst           = 0x02
	cmdData                = 0x03
	cmdClose               = 0x04
	cmdCaps                = 0x05
	cmdDataFirstCompressed = 0x06
	cmdDataCompressed      = 0x07
	cmdSoftSync            = 0x08

	chanFirst      = 0x01 // CHANNEL_FLAG_FIRST
	chanLast       = 0x02 // CHANNEL_FLAG_LAST
	chanCompressed = 0x00200000

	// MaxPDU is the largest DVC PDU this client sends; it fits one static channel chunk
	// (CHANNEL_CHUNK_LENGTH).
	MaxPDU = 1600

	maxStaticMessage = 1 << 20  // one DVC PDU fits a chunk; this only stops abuse
	maxMessage       = 32 << 20 // reassembled DATA_FIRST/DATA message
	maxChannels      = 64

	statusOK          = 0
	statusNoListener  = 0xC0000001 // any failure HRESULT refuses the channel
	capsVersionClient = 2          // never 3: compressed DVC data is not supported
)

// Channel is one open dynamic channel. Open runs right after the create response was sent and gets
// a writer for the channel; Data gets every reassembled message; Close runs when the server closes
// the channel or the dynamic channels are reset.
type Channel interface {
	Open(write func([]byte) error) error
	Data(msg []byte) error
	Close()
}

// Client is the client side of drdynvc. Send writes one static channel chunk, CHANNEL_PDU_HEADER
// included; Accept returns the Channel for a requested name, or nil to refuse it. Receive is not
// safe for concurrent use (it runs on the session's receive loop); writes from channels are.
type Client struct {
	Send   func(chunk []byte) error
	Accept func(name string) Channel

	static     []byte // static chunk reassembly
	staticWant int
	open       map[uint32]*dvc
}

type dvc struct {
	name string
	ch   Channel
	buf  []byte
	want int // total length announced by DATA_FIRST, 0 when not reassembling
}

var errShort = errors.New("drdynvc: truncated PDU")

// Receive handles one chunk of the static channel (CHANNEL_PDU_HEADER included). An error is
// fatal for the session.
func (c *Client) Receive(chunk []byte) error {
	if len(chunk) < 8 {
		return errShort
	}
	total := int(binary.LittleEndian.Uint32(chunk))
	flags := binary.LittleEndian.Uint32(chunk[4:])
	body := chunk[8:]
	if flags&chanCompressed != 0 {
		return errors.New("drdynvc: compressed static channel data was not negotiated")
	}
	if flags&chanFirst != 0 {
		if total > maxStaticMessage || total < len(body) {
			return fmt.Errorf("drdynvc: static message of %d bytes", total)
		}
		c.static, c.staticWant = c.static[:0], total
	} else if c.staticWant == 0 {
		return errors.New("drdynvc: static chunk without a first chunk")
	}
	if len(c.static)+len(body) > c.staticWant {
		return errors.New("drdynvc: static chunks exceed the announced length")
	}
	c.static = append(c.static, body...)
	if flags&chanLast == 0 {
		return nil
	}
	if len(c.static) != c.staticWant {
		return errors.New("drdynvc: static message shorter than announced")
	}
	c.staticWant = 0
	return c.handle(c.static)
}

// channelID reads a cbChId-sized id.
func channelID(p []byte, cb byte) (uint32, []byte, error) {
	n := [4]int{1, 2, 4, 0}[cb&3]
	if n == 0 || len(p) < n {
		return 0, nil, errShort
	}
	var id uint32
	switch n {
	case 1:
		id = uint32(p[0])
	case 2:
		id = uint32(binary.LittleEndian.Uint16(p))
	default:
		id = binary.LittleEndian.Uint32(p)
	}
	return id, p[n:], nil
}

// putID appends id in the smallest width and returns its cbChId.
func putID(b []byte, id uint32) ([]byte, byte) {
	switch {
	case id <= 0xff:
		return append(b, byte(id)), 0
	case id <= 0xffff:
		return binary.LittleEndian.AppendUint16(b, uint16(id)), 1
	default:
		return binary.LittleEndian.AppendUint32(b, id), 2
	}
}

func (c *Client) handle(p []byte) error {
	if len(p) < 1 {
		return errShort
	}
	hdr := p[0]
	cmd, sp, cb := hdr>>4, hdr>>2&3, hdr&3
	p = p[1:]
	switch cmd {
	case cmdCaps:
		if len(p) < 3 {
			return errShort
		}
		// a (new) caps request starts the dynamic channels over (also after a reactivation)
		c.reset()
		return c.write([]byte{cmdCaps << 4, 0, capsVersionClient, 0})
	case cmdCreate:
		id, rest, err := channelID(p, cb)
		if err != nil {
			return err
		}
		name := rest
		for i, b := range rest {
			if b == 0 {
				name = rest[:i]
				break
			}
		}
		return c.create(id, string(name))
	case cmdDataFirst, cmdData:
		id, rest, err := channelID(p, cb)
		if err != nil {
			return err
		}
		d := c.open[id]
		if d == nil {
			return nil // data for a channel that is not open (refused or closed): dropped
		}
		if cmd == cmdDataFirst {
			n := [4]int{1, 2, 4, 0}[sp]
			if n == 0 || len(rest) < n {
				return errShort
			}
			var total int
			switch n {
			case 1:
				total = int(rest[0])
			case 2:
				total = int(binary.LittleEndian.Uint16(rest))
			default:
				total = int(binary.LittleEndian.Uint32(rest))
			}
			rest = rest[n:]
			if total > maxMessage || total < len(rest) {
				return fmt.Errorf("drdynvc: message of %d bytes on %q", total, d.name)
			}
			if total == len(rest) {
				d.want = 0
				return d.ch.Data(rest)
			}
			d.buf, d.want = append(d.buf[:0], rest...), total
			return nil
		}
		if d.want == 0 {
			return d.ch.Data(rest)
		}
		if len(d.buf)+len(rest) > d.want {
			return fmt.Errorf("drdynvc: data exceeds the announced length on %q", d.name)
		}
		d.buf = append(d.buf, rest...)
		if len(d.buf) < d.want {
			return nil
		}
		d.want = 0
		return d.ch.Data(d.buf)
	case cmdClose:
		id, _, err := channelID(p, cb)
		if err != nil {
			return err
		}
		if d := c.open[id]; d != nil {
			delete(c.open, id)
			d.ch.Close()
			b, cbID := putID([]byte{0}, id)
			b[0] = cmdClose<<4 | cbID
			return c.write(b)
		}
		return nil
	case cmdDataFirstCompressed, cmdDataCompressed:
		return errors.New("drdynvc: compressed dynamic channel data was not negotiated")
	default: // soft sync (multitransport, never advertised) and unknown commands
		return nil
	}
}

func (c *Client) create(id uint32, name string) error {
	var ch Channel
	if c.Accept != nil && len(c.open) < maxChannels {
		ch = c.Accept(name)
	}
	status := uint32(statusOK)
	if ch == nil {
		status = statusNoListener
	}
	if old := c.open[id]; old != nil { // the id is reused: the old channel is gone
		delete(c.open, id)
		old.ch.Close()
	}
	b, cbID := putID([]byte{0}, id)
	b[0] = cmdCreate<<4 | cbID
	if err := c.write(binary.LittleEndian.AppendUint32(b, status)); err != nil || ch == nil {
		return err
	}
	if c.open == nil {
		c.open = map[uint32]*dvc{}
	}
	c.open[id] = &dvc{name: name, ch: ch}
	return ch.Open(func(msg []byte) error { return c.writeData(id, msg) })
}

// reset closes every open dynamic channel.
func (c *Client) reset() {
	for id, d := range c.open {
		delete(c.open, id)
		d.ch.Close()
	}
}

// write sends one DVC PDU as a single static channel chunk.
func (c *Client) write(pdu []byte) error {
	chunk := make([]byte, 8, 8+len(pdu))
	binary.LittleEndian.PutUint32(chunk, uint32(len(pdu)))
	binary.LittleEndian.PutUint32(chunk[4:], chanFirst|chanLast)
	return c.Send(append(chunk, pdu...))
}

// writeData sends msg on channel id: one DATA PDU when it fits, else DATA_FIRST and DATA PDUs.
func (c *Client) writeData(id uint32, msg []byte) error {
	hdr, cbID := putID([]byte{0}, id)
	if len(hdr)+len(msg) <= MaxPDU {
		hdr[0] = cmdData<<4 | cbID
		return c.write(append(hdr, msg...))
	}
	first := append([]byte(nil), hdr...)
	first[0] = cmdDataFirst<<4 | 2<<2 | cbID
	first = binary.LittleEndian.AppendUint32(first, uint32(len(msg)))
	n := MaxPDU - len(first)
	if err := c.write(append(first, msg[:n]...)); err != nil {
		return err
	}
	for msg = msg[n:]; len(msg) > 0; {
		n = min(MaxPDU-len(hdr), len(msg))
		pdu := append([]byte(nil), hdr...)
		pdu[0] = cmdData<<4 | cbID
		if err := c.write(append(pdu, msg[:n]...)); err != nil {
			return err
		}
		msg = msg[n:]
	}
	return nil
}
