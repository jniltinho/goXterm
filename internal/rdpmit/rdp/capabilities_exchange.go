package rdp

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"rustxterm-go/internal/rdpmit/logging"
	"rustxterm-go/internal/rdpmit/protocol/pdu"
)

// maxSkippedBeforeDemandActive bounds the PDUs skipped while waiting for the Demand Active.
const maxSkippedBeforeDemandActive = 64

func (c *Client) capabilitiesExchange() error {
	// rustxterm: wait for the Demand Active, skipping any other PDU the server sends first (upstream
	// decoded the first PDU as a Demand Active and failed with EOF on anything else).
	var resp pdu.ServerDemandActive
	for skipped := 0; ; skipped++ {
		_, wire, err := c.mcsLayer.Receive()
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("the server closed the connection before the session started (%w)", err)
		}
		if err != nil {
			return err
		}
		b, err := io.ReadAll(wire)
		if err != nil {
			return err
		}
		var h pdu.ShareControlHeader
		if h.Deserialize(bytes.NewReader(b)) == nil && h.PDUType.IsDemandActive() {
			if err = resp.Deserialize(bytes.NewReader(b)); err != nil {
				return fmt.Errorf("demand active: %w", err)
			}
			break
		}
		if skipped >= maxSkippedBeforeDemandActive {
			return errors.New("no Demand Active from the server")
		}
		logging.Debug("RDP: skipping PDU type 0x%04X (%d bytes) before Demand Active", uint16(h.PDUType), len(b))
	}

	c.shareID = resp.ShareID
	c.serverCapabilitySets = resp.CapabilitySets

	req := pdu.NewClientConfirmActive(resp.ShareID, c.userID, c.desktopWidth, c.desktopHeight, false)
	for _, cs := range req.CapabilitySets { // rustxterm: same layout as the core data
		if cs.InputCapabilitySet != nil && c.keyboardLayoutSet {
			cs.InputCapabilitySet.KeyboardLayout = c.keyboardLayout
		}
	}

	// rustxterm: only RDP 8+ servers (Windows 8 / 2012 and later); xrdp and 2008 R2 report RDP 5–7,
	// and xrdp switches to GDI orders this client does not draw when surface commands are offered.
	if c.enableRFX && c.serverVersion >= 0x00080005 {
		// Set MultifragmentUpdate MaxRequestSize large enough for RFX tiles
		for i, cap := range req.CapabilitySets {
			if cap.MultifragmentUpdateCapabilitySet != nil {
				req.CapabilitySets[i].MultifragmentUpdateCapabilitySet.MaxRequestSize = 0x200000 // 2MB
				break
			}
		}
		req.CapabilitySets = append(req.CapabilitySets,
			pdu.NewSurfaceCommandsCapabilitySet(),
			pdu.NewBitmapCodecsWithRFXCapabilitySet(),
		)
	}

	return c.mcsLayer.Send(c.userID, c.channelIDMap["global"], req.Serialize())
}
