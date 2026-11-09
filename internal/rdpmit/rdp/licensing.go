// rustxterm addition (not in upstream go-rdp): answer a server License Request.

package rdp

import (
	"crypto/rand"
	"encoding/binary"

	"rustxterm-go/internal/rdpmit/codec"
)

const (
	licenseRequest    = 0x01 // LICENSE_REQUEST
	newLicenseRequest = 0x13 // NEW_LICENSE_REQUEST
	preambleVersion3  = 0x03 // PREAMBLE_VERSION_3_0
	secLicensePkt     = 0x0080
)

// newLicenseRequestPDU builds a Client New License Request (MS-RDPELE 2.2.2.2).
// shortcut: the pre-master secret blob is left empty (it should be encrypted to the key in the
// server's License Request), and the Platform Challenge that follows on servers with a license
// server is not handled; xrdp, and Windows hosts that answer STATUS_VALID_CLIENT straight away,
// need neither. Implement MS-RDPELE in full if per-device/user CAL servers must be supported.
func newLicenseRequestPDU(user, machine string, clientRandom []byte) []byte {
	blob := func(b []byte, typ uint16, data []byte) []byte {
		b = binary.LittleEndian.AppendUint16(b, typ)
		b = binary.LittleEndian.AppendUint16(b, uint16(len(data))) // #nosec G115 -- short names
		return append(b, data...)
	}
	var body []byte
	body = binary.LittleEndian.AppendUint32(body, 0x00000001) // KEY_EXCHANGE_ALG_RSA
	body = binary.LittleEndian.AppendUint32(body, 0x04010000) // CLIENT_OS_ID_WINNT_POST_52 | CLIENT_IMAGE_ID_MICROSOFT
	body = append(body, clientRandom...)
	body = blob(body, 0x0002, nil)                        // BB_RANDOM_BLOB (EncryptedPreMasterSecret)
	body = blob(body, 0x000F, append([]byte(user), 0))    // BB_CLIENT_USER_NAME_BLOB
	body = blob(body, 0x0010, append([]byte(machine), 0)) // BB_CLIENT_MACHINE_NAME_BLOB
	pdu := []byte{newLicenseRequest, preambleVersion3}
	pdu = binary.LittleEndian.AppendUint16(pdu, uint16(4+len(body))) // #nosec G115
	return codec.WrapSecurityFlag(secLicensePkt, append(pdu, body...))
}

func (c *Client) sendNewLicenseRequest() error {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	_, user := c.parseDomainUser()
	p := newLicenseRequestPDU(user, "rustxterm", random)
	if c.stdSec != nil { // standard RDP security
		return c.stdSec.sendLicense(c.userID, c.channelIDMap["global"], p[4:])
	}
	return c.mcsLayer.Send(c.userID, c.channelIDMap["global"], p)
}
