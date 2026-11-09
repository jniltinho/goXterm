package tpkt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

// Receive reads one TPKT packet and returns its payload.
// rustxterm: full reads (upstream used a single Read for the header and for the payload; a short
// read from the buffered connection left the end of a large PDU zeroed, which showed as bad MACs
// under standard RDP security and silently corrupted data under TLS).
func (p *Protocol) Receive() (io.Reader, error) {
	header := make([]byte, headerLen)
	if _, err := io.ReadFull(p.conn, header); err != nil {
		return nil, err
	}

	n := binary.BigEndian.Uint16(header[2:4])
	if n < headerLen {
		return nil, errors.New("tpkt: packet shorter than its header")
	}

	data := make([]byte, n-headerLen)
	if _, err := io.ReadFull(p.conn, data); err != nil {
		return nil, err
	}

	return bytes.NewBuffer(data), nil
}
