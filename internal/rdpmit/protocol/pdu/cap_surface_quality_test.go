// rustxterm addition: the NSCodec properties offered to the server.

package pdu

import (
	"bytes"
	"testing"
)

// TestNSCodecLeastLossy: fAllowDynamicFidelity 0, fAllowSubsampling 0, colorLossLevel 1 (MS-RDPNSC 2.2.1).
func TestNSCodecLeastLossy(t *testing.T) {
	c := NewBitmapCodecsWithRFXCapabilitySet().BitmapCodecsCapabilitySet.BitmapCodecArray[0]
	if !bytes.Equal(c.CodecProperties, []byte{0, 0, 1}) {
		t.Fatalf("NSCodec properties %v", c.CodecProperties)
	}
}
