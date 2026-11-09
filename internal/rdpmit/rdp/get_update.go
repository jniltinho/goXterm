package rdp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"rustxterm-go/internal/rdpmit/logging"
	"rustxterm-go/internal/rdpmit/protocol/pdu"
)

// updateCounter tracks total updates processed (for debugging/metrics)
var updateCounter atomic.Int64

// GetUpdate reads the next screen update from the RDP server.
// The returned Update contains raw bitmap data for rendering.
// rustxterm: a loop instead of upstream's recursion, so a long run of PDUs that carry no bitmap
// (graphics pipeline channel traffic, for one) cannot grow the stack.
func (c *Client) GetUpdate() (*Update, error) {
	// If we have a pending slow-path update, return it first
	if c.pendingSlowPathUpdate != nil {
		update := c.pendingSlowPathUpdate
		c.pendingSlowPathUpdate = nil
		return update, nil
	}

	for {
		protocol, err := receiveProtocol(c.buffReader)
		if err != nil {
			return nil, err
		}

		updateCounter.Add(1)

		if !protocol.IsX224() {
			fpUpdate, err := c.fastPath.Receive()
			if err != nil {
				return nil, err
			}
			// FastPath updates are handed over as received: [updateHeader:1] [size:2] [data...]
			return &Update{Data: fpUpdate.Data}, nil
		}

		update, err := c.getX224Update()
		switch {
		case err == nil:
			if update != nil {
				// Got a converted slow-path bitmap update
				return update, nil
			}
			// Non-bitmap X224 update (or channel data): read the next PDU
		case errors.Is(err, pdu.ErrDeactivateAll):
			// rustxterm: deactivation-reactivation (MS-RDPBCGR 1.3.1.3): Windows deactivates right
			// after the logon (2008 R2 always) and sends a new Demand Active; redo the capabilities
			// exchange and finalization and keep the session instead of closing it.
			c.inactive.Store(true)
			if err := c.reactivate(); err != nil {
				return nil, fmt.Errorf("reactivation: %w", err)
			}
		default:
			return nil, fmt.Errorf("get X.224 update: %w", err)
		}
	}
}

// Slow-path update types
const (
	SlowPathUpdateTypeOrders      uint16 = 0x0000
	SlowPathUpdateTypeBitmap      uint16 = 0x0001
	SlowPathUpdateTypePalette     uint16 = 0x0002
	SlowPathUpdateTypeSynchronize uint16 = 0x0003
)

// Fastpath update codes (for conversion)
const (
	FastPathUpdateCodeBitmap      uint8 = 0x01
	FastPathUpdateCodePalette     uint8 = 0x02
	FastPathUpdateCodeSynchronize uint8 = 0x03
)

func (c *Client) getX224Update() (*Update, error) {
	channelID, wire, err := c.mcsLayer.Receive()
	if err != nil {
		return nil, err
	}

	// rustxterm: static virtual channels registered with AddStaticChannel (e.g. cliprdr) get the
	// raw channel chunk, CHANNEL_PDU_HEADER included.
	if handled, err := c.dispatchChannel(channelID, wire); handled {
		return nil, err
	}

	// Read ShareControlHeader first to check PDU type
	var shareControlHeader pdu.ShareControlHeader
	if err = shareControlHeader.Deserialize(wire); err != nil {
		return nil, err
	}

	if shareControlHeader.PDUType.IsDeactivateAll() {
		return nil, pdu.ErrDeactivateAll
	}

	// Read ShareDataHeader fields
	var shareID uint32
	var padding uint8
	var streamID uint8
	var uncompressedLength uint16
	var pduType2 pdu.Type2
	var compressedType uint8
	var compressedLength uint16

	if err := binary.Read(wire, binary.LittleEndian, &shareID); err != nil {
		return nil, fmt.Errorf("read shareID: %w", err)
	}
	if err := binary.Read(wire, binary.LittleEndian, &padding); err != nil {
		return nil, fmt.Errorf("read padding: %w", err)
	}
	if err := binary.Read(wire, binary.LittleEndian, &streamID); err != nil {
		return nil, fmt.Errorf("read streamID: %w", err)
	}
	if err := binary.Read(wire, binary.LittleEndian, &uncompressedLength); err != nil {
		return nil, fmt.Errorf("read uncompressedLength: %w", err)
	}
	if err := binary.Read(wire, binary.LittleEndian, &pduType2); err != nil {
		return nil, fmt.Errorf("read pduType2: %w", err)
	}
	if err := binary.Read(wire, binary.LittleEndian, &compressedType); err != nil {
		return nil, fmt.Errorf("read compressedType: %w", err)
	}
	if err := binary.Read(wire, binary.LittleEndian, &compressedLength); err != nil {
		return nil, fmt.Errorf("read compressedLength: %w", err)
	}

	// Handle bitmap updates (PDUTYPE2_UPDATE = 0x02)
	if pduType2.IsUpdate() {
		return c.handleSlowPathGraphicsUpdate(wire)
	}

	// Handle error info
	if pduType2.IsErrorInfo() {
		var errorInfo pdu.ErrorInfoPDUData
		if err := errorInfo.Deserialize(wire); err != nil {
			logging.Warn("Error deserializing error info PDU: %v", err)
		} else {
			logging.Warn("Received error info: %s", errorInfo.String())
		}
	}

	return nil, nil
}

func (c *Client) handleSlowPathGraphicsUpdate(wire io.Reader) (*Update, error) {
	// Read updateType (2 bytes) - [MS-RDPBCGR] 2.2.9.1.1.3 Slow-Path Graphics Update
	var updateType uint16
	if err := binary.Read(wire, binary.LittleEndian, &updateType); err != nil {
		return nil, err
	}

	// Read the rest of the data (bitmap data including numberRectangles)
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, wire); err != nil && err != io.EOF {
		return nil, err
	}
	updateData := buf.Bytes()

	// Convert to fastpath format for the browser
	// The JavaScript parseBitmapUpdate expects: [updateType (2 bytes)] [numberRectangles (2 bytes)] [bitmap data...]
	// So we need to include the updateType in the data we send

	var fastpathCode uint8
	switch updateType {
	case SlowPathUpdateTypeBitmap:
		fastpathCode = FastPathUpdateCodeBitmap
	case SlowPathUpdateTypePalette:
		fastpathCode = FastPathUpdateCodePalette
	case SlowPathUpdateTypeSynchronize:
		fastpathCode = FastPathUpdateCodeSynchronize
	default:
		// Unknown update type, skip
		return nil, nil
	}

	// Build fastpath-style data for the browser
	// Format: [updateHeader (1 byte)] [size (2 bytes LE)] [updateType (2 bytes LE)] [bitmap data...]
	// The size field should be the size of everything after the updateHeader+size, i.e. updateType + bitmapData
	updateHeader := fastpathCode                 // fragmentation=0 (single), compression=0 (none)
	totalDataSize := uint16(2 + len(updateData)) // #nosec G115

	fpData := make([]byte, 3+2+len(updateData))
	fpData[0] = updateHeader
	binary.LittleEndian.PutUint16(fpData[1:3], totalDataSize)
	binary.LittleEndian.PutUint16(fpData[3:5], updateType)
	copy(fpData[5:], updateData)

	return &Update{Data: fpData}, nil
}

// reactivate runs the capabilities exchange and connection finalization again after a Deactivate
// All, then asks for a full redraw.
func (c *Client) reactivate() error {
	if err := c.capabilitiesExchange(); err != nil {
		return err
	}
	if err := c.connectionFinalization(); err != nil {
		return err
	}
	c.inactive.Store(false)
	if err := c.sendRefreshRect(); err != nil {
		logging.Warn("RDP: refresh after reactivation: %v", err)
	}
	return nil
}
