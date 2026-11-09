package rdp

import "rustxterm-go/internal/rdpmit/protocol/fastpath"

// SendInputEvent sends a FastPath input event (mouse, keyboard, etc.) to the server.
// rustxterm: input is dropped while the session is deactivated (between Deactivate All and the
// end of reactivation), as the protocol requires.
func (c *Client) SendInputEvent(data []byte) error {
	if c.inactive.Load() {
		return nil
	}
	if s := c.stdSec; s != nil { // rustxterm: standard RDP security (stdsec.go)
		s.mu.Lock()
		defer s.mu.Unlock()
		p := fastpath.NewInputEventPDU(s.seal(data))
		p.SetFlags(fastPathEncrypted)
		return c.fastPath.Send(p)
	}
	return c.fastPath.Send(fastpath.NewInputEventPDU(data))
}
