package rdp

// Close closes the RDP connection and releases resources.
func (c *Client) Close() error {
	c.mu.Lock() // rustxterm: reconnect may be swapping the connection
	defer c.mu.Unlock()
	c.closed = true
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
