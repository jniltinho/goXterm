package rdp

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/bodgit/ntlmssp"
)

// TestAuthenticateSafelyMalformedChallenge: a challenge whose MsvAvFlags AV pair is 1 byte long
// (it is a 32-bit field) panics inside bodgit/ntlmssp; authenticateSafely turns it into an error.
func TestAuthenticateSafelyMalformedChallenge(t *testing.T) {
	var info []byte
	av := func(id uint16, v []byte) {
		info = binary.LittleEndian.AppendUint16(info, id)
		info = binary.LittleEndian.AppendUint16(info, uint16(len(v)))
		info = append(info, v...)
	}
	av(7, make([]byte, 8)) // MsvAvTimestamp
	av(6, []byte{1})       // MsvAvFlags, too short
	av(0, nil)             // MsvAvEOL

	const hdr = 56 // with the 8-byte version
	msg := append([]byte("NTLMSSP\x00"), 2, 0, 0, 0)
	msg = append(msg, 0, 0, 0, 0, hdr, 0, 0, 0)             // TargetName: empty at the payload
	msg = binary.LittleEndian.AppendUint32(msg, 0xE2898215) // flags incl. unicode, NTLM, target info, ESS, version
	msg = append(msg, 1, 2, 3, 4, 5, 6, 7, 8)               // server challenge
	msg = append(msg, make([]byte, 8)...)                   // reserved
	msg = binary.LittleEndian.AppendUint16(msg, uint16(len(info)))
	msg = binary.LittleEndian.AppendUint16(msg, uint16(len(info)))
	msg = binary.LittleEndian.AppendUint32(msg, hdr)
	msg = append(msg, 6, 1, 0xB1, 0x1D, 0, 0, 0, 15) // version
	msg = append(msg, info...)

	c, err := ntlmssp.NewClient(ntlmssp.SetUserInfo("u", "p"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Authenticate(nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = authenticateSafely(c, msg); err == nil || !strings.Contains(err.Error(), "malformed NTLM challenge") {
		t.Fatalf("err = %v, want the malformed-challenge error", err)
	}
}
