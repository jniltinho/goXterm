package rdp

import "rustxterm-go/internal/rdpmit/protocol/fastpath"

// SendInputEvent sends a FastPath input event (mouse, keyboard, etc.) to the server.
// rustxterm: input is dropped while the session is deactivated (between Deactivate All and the
// end of reactivation), as the protocol requires.
func (c *Client) SendInputEvent(data []byte) error {
	if c.inactive.Load() {
		return nil
	}
	return c.fastPath.Send(fastpath.NewInputEventPDU(data))
}
