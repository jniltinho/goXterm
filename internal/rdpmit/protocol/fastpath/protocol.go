// Package fastpath implements the RDP Fast-Path protocol as specified in MS-RDPBCGR.
// Fast-Path provides optimized encoding for input and output PDUs.
package fastpath

import (
	"io"
)

type Protocol struct {
	conn io.ReadWriter

	updatePDUData []byte

	// Decrypt (rustxterm), under standard RDP security, gets every PDU's data: it opens an
	// encrypted one (MAC + RC4) and may refuse a clear one. nil refuses encrypted PDUs.
	Decrypt func(encrypted, saltedMAC bool, data []byte) ([]byte, error)
}

func New(conn io.ReadWriter) *Protocol {
	return &Protocol{
		conn: conn,

		updatePDUData: make([]byte, 64*1024),
	}
}
