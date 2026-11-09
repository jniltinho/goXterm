package rdp

import (
	"bufio"
	"errors"
	"testing"
)

// x224Bytes is an endless stream of TPKT version bytes: every PDU looks like slow-path, and the fake
// MCS layer supplies the content.
type x224Bytes struct{}

func (x224Bytes) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0x03
	}
	return len(p), nil
}

// TestChannelOnlyTrafficLoopsAndFails: GetUpdate keeps reading while PDUs only carry channel data,
// and a channel handler's error ends it.
func TestChannelOnlyTrafficLoopsAndFails(t *testing.T) {
	const n = 50000
	f := &fakeMCS{}
	for i := 0; i < n; i++ {
		f.recv = append(f.recv, []byte{8, 0, 0, 0, 3, 0, 0, 0})
	}
	stop := errors.New("graphics failure")
	calls := 0
	c := &Client{mcsLayer: f, buffReader: bufio.NewReader(x224Bytes{}), channelIDMap: map[string]uint16{"drdynvc": 1003}}
	c.AddStaticChannel("drdynvc", 0, func([]byte) error {
		if calls++; calls == n {
			return stop
		}
		return nil
	})
	if _, err := c.GetUpdate(); !errors.Is(err, stop) || calls != n {
		t.Fatalf("GetUpdate: %v after %d chunks", err, calls)
	}
}
