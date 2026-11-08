// rustxterm addition (not in upstream go-rdp): caller-supplied TLS settings and static virtual
// channels, so a client outside this package can run its own channel (e.g. cliprdr) and decide
// certificate trust during the handshake.

package rdp

import (
	"crypto/tls"
	"fmt"
	"io"
)

// SetTLSClientConfig makes StartTLS and NLA use cfg (cloned) instead of the built-in settings.
// Set it before Connect.
func (c *Client) SetTLSClientConfig(cfg *tls.Config) { c.tlsConfig = cfg }

// AddStaticChannel requests the static virtual channel name with CHANNEL_DEF options; recv gets
// every chunk the server sends on it (CHANNEL_PDU_HEADER included). Call it before Connect.
func (c *Client) AddStaticChannel(name string, options uint32, recv func([]byte)) {
	if c.channelOptions == nil {
		c.channelOptions = map[string]uint32{}
		c.channelHandlers = map[string]func([]byte){}
	}
	if _, ok := c.channelHandlers[name]; !ok {
		c.channels = append(c.channels, name)
	}
	c.channelOptions[name] = options
	c.channelHandlers[name] = recv
}

// SendStaticChannel writes one channel chunk (CHANNEL_PDU_HEADER included) on the named channel.
func (c *Client) SendStaticChannel(name string, chunk []byte) error {
	id, ok := c.channelIDMap[name]
	if !ok || id == 0 {
		return fmt.Errorf("static channel %q was not joined", name)
	}
	return c.mcsLayer.Send(c.userID, id, chunk)
}

// dispatchChannel hands a PDU received on a registered static channel to its handler and reports
// whether it did. Every receive loop calls it (updates, the wait for Demand Active, finalization),
// so channel traffic during (re)activation — e.g. cliprdr Monitor Ready — is not lost.
func (c *Client) dispatchChannel(channelID uint16, wire io.Reader) (bool, error) {
	for name, recv := range c.channelHandlers {
		if id, ok := c.channelIDMap[name]; ok && id == channelID {
			data, err := io.ReadAll(wire)
			if err != nil {
				return true, err
			}
			recv(data)
			return true, nil
		}
	}
	return false, nil
}
