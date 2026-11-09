// rustxterm addition: tests for standard RDP security (stdsec.go).

package rdp

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"

	"rustxterm-go/internal/rdpmit/protocol/fastpath"
	"rustxterm-go/internal/rdpmit/protocol/pdu"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Client random of MS-RDPBCGR 4.8 and server random of 4.1.4.
const (
	specClientRandom = "ffee" + "00000000000000000000000000000000000000000000000000000000" + "00ff"
	specServerRandom = "1011772030610a12e434a11ef2c39f317da45f01893496e0ff1108697f1ac3d2"
)

// TestSessionKeysKnownAnswer: the keys, MAC, RC4 and key update match values computed with rdesktop's
// sec_generate_keys / sec_sign / sec_update algorithm (an independent implementation of
// MS-RDPBCGR 5.3.5–5.3.7) for the spec's sample randoms.
func TestSessionKeysKnownAnswer(t *testing.T) {
	cr, sr := unhex(t, specClientRandom), unhex(t, specServerRandom)
	mac, enc, dec := sessionKeys(cr, sr)
	for name, got := range map[string][]byte{
		"ec531a8b7aad52da47918d3e3ac7eb10": mac,
		"98d1265c3a1ba2469d7a2acfbc0fafd9": enc,
		"b99465830113eabd70a17644c9bc8a24": dec,
	} {
		if hex.EncodeToString(got) != name {
			t.Fatalf("key %x, want %s", got, name)
		}
	}
	msg := []byte("goXterm standard RDP security")
	if got := hex.EncodeToString(macSignature(mac, msg, nil)); got != "5b8576da96c838d2" {
		t.Fatalf("MAC %s", got)
	}
	s := newRC4Stream(enc)
	b := append([]byte{}, msg...)
	s.xor(b)
	if got := hex.EncodeToString(b); got != "e95ab196ea48d6de1c24860757fa24135c3b35fb9f884f22246727e926" {
		t.Fatalf("RC4 %s", got)
	}
	if got := hex.EncodeToString(updateKey(enc, enc)); got != "5fc716d0c3ecde44fb0d4b5688f04878" {
		t.Fatalf("key update %s", got)
	}
}

// TestEncryptClientRandomSpec: MS-RDPBCGR 4.8's 512-bit key pair; the encrypted client random is
// little-endian, 64 + 8 bytes, and the private exponent turns it back into the random.
func TestEncryptClientRandomSpec(t *testing.T) {
	mod := unhex(t, "37a870fe9ab9a854cb9879447ab9eb3806ea26a147ea19705df352887021b59e"+
		"50b4e1f51ad82d514d1aad797c8946b0cc667402d8285d9dd7cafc600f38f9b3")
	priv := unhex(t, "c107e7d4d3388d36f59e8b960d5565082825a32ec768d644852d32f672a89bba"+
		"5e8282f05c0ceb6b126aa74515ce41e003e5e66ddffd58610b07a47bb3f37194")
	k := &serverKey{n: new(big.Int).SetBytes(reversed(mod)), e: 0x10001, size: 64}
	random := unhex(t, specClientRandom)
	enc := k.encryptClientRandom(random)
	if len(enc) != 72 || !bytes.Equal(enc[64:], make([]byte, 8)) {
		t.Fatalf("encrypted random %d bytes, tail % x", len(enc), enc[64:])
	}
	m := new(big.Int).Exp(new(big.Int).SetBytes(reversed(enc[:64])), new(big.Int).SetBytes(reversed(priv)), k.n)
	if got := reversed(m.FillBytes(make([]byte, 32))); !bytes.Equal(got, random) {
		t.Fatalf("decrypted % x", got)
	}
}

// TestServerSecurityDataSpec parses the server user data of MS-RDPBCGR 4.1.4 (proprietary
// certificate, 512-bit key, 128-bit RC4).
func TestServerSecurityDataSpec(t *testing.T) {
	ud := unhex(t, "010c0c000400080000000000030c1000eb030300ec03ed03ee030000020cec00020000000200000020000000b8000000"+
		specServerRandom+"01000000010000000100000006005c005253413148000000000200003f00000001000100"+
		"cb81feba6d61c35505d55f2e87f87194d6f1a5cbf15f0c3df8700296c4fb9bc83c2d55aee8ff3275ea6879e5a201fd31a0b11f55a61fc1f6d1838863265612bc"+
		"000000000000000008004800e9e1d628468b4ef50adffdee2199acb4e18f5f815782ef9d965263271829dbb34afd9ada42adb56921890e1dc04c1aa8"+
		"aa713e0f54b99ae499683f6cd67684610000000000000000")
	var sud pdu.ServerUserData
	if err := sud.Deserialize(bytes.NewReader(ud)); err != nil {
		t.Fatal(err)
	}
	if err := checkServerSecurity(sud.ServerSecurityData); err != nil {
		t.Fatal(err)
	}
	if sud.ServerNetworkData == nil || sud.ServerNetworkData.MCSChannelId != 1003 {
		t.Fatal("the blocks after the security data were not parsed")
	}
	k, err := serverPublicKey(sud.ServerSecurityData.ServerCertificate)
	if err != nil {
		t.Fatal(err)
	}
	if k.size != 64 || k.e != 0x10001 || k.n.BitLen() != 512 || k.n.Bytes()[63] != 0xcb {
		t.Fatalf("key size %d e %#x bits %d", k.size, k.e, k.n.BitLen())
	}
	for _, sd := range []*pdu.ServerSecurityData{
		nil, {EncryptionMethod: 0, EncryptionLevel: 0}, {EncryptionMethod: pdu.EncryptionFlag40Bit, EncryptionLevel: 2, ServerRandom: make([]byte, 32)},
	} {
		if checkServerSecurity(sd) == nil {
			t.Fatalf("accepted %+v", sd)
		}
	}
}

// serverSide is the server's view of the keys: it decrypts with the client's encrypt key.
func serverSide(clientRandom, serverRandom []byte) *stdSecurity {
	mac, enc, dec := sessionKeys(clientRandom, serverRandom)
	return &stdSecurity{macKey: mac, enc: newRC4Stream(dec), dec: newRC4Stream(enc)}
}

func TestStdSecurityRoundTrip(t *testing.T) {
	cr, sr := unhex(t, specClientRandom), unhex(t, specServerRandom)
	cli, srv := newStdSecurity(nil, cr, sr), serverSide(cr, sr)
	for i := range 2*keyUpdateInterval + 3 { // crosses two key updates
		msg := []byte{byte(i), byte(i >> 8), 'x', 'y'}
		got, err := srv.open(cli.seal(msg), false)
		if err != nil || !bytes.Equal(got, msg) {
			t.Fatalf("packet %d: % x %v", i, got, err)
		}
	}
	// Server to client with a salted MAC (count = packets decrypted before).
	msg := []byte("update")
	cli2, srv2 := newStdSecurity(nil, cr, sr), serverSide(cr, sr)
	cli2.decCount = 7
	sealed := append(macSignature(srv2.macKey, msg, []byte{7, 0, 0, 0}), msg...)
	srv2.enc.xor(sealed[8:])
	if got, err := cli2.open(sealed, true); err != nil || !bytes.Equal(got, msg) {
		t.Fatalf("salted: %q %v", got, err)
	}
	// A changed byte fails the MAC.
	cli3, srv3 := newStdSecurity(nil, cr, sr), serverSide(cr, sr)
	bad := cli3.seal([]byte("logon"))
	bad[9] ^= 1
	if _, err := srv3.open(bad, false); err == nil {
		t.Fatal("tampered PDU accepted")
	}
}

// fakeMCS is the server side of the MCS layer for the handshake test.
type fakeMCS struct {
	userData []byte
	sent     [][]byte
	recv     [][]byte
}

func (f *fakeMCS) Send(_, _ uint16, data []byte) error {
	f.sent = append(f.sent, append([]byte{}, data...))
	return nil
}

func (f *fakeMCS) Receive() (uint16, io.Reader, error) {
	if len(f.recv) == 0 {
		return 0, nil, io.EOF
	}
	b := f.recv[0]
	f.recv = f.recv[1:]
	return 1003, bytes.NewReader(b), nil
}
func (f *fakeMCS) Connect([]byte) (io.Reader, error)            { return bytes.NewReader(f.userData), nil }
func (f *fakeMCS) ErectDomain() error                           { return nil }
func (f *fakeMCS) AttachUser() (uint16, error)                  { return 1007, nil }
func (f *fakeMCS) JoinChannels(uint16, map[string]uint16) error { return nil }

func serverUserData(key *rsa.PublicKey, serverRandom []byte) []byte {
	mod := reversed(key.N.Bytes())
	var cert []byte
	cert = binary.LittleEndian.AppendUint32(cert, 1) // CERT_CHAIN_VERSION_1
	cert = binary.LittleEndian.AppendUint32(cert, 1) // SIGNATURE_ALG_RSA
	cert = binary.LittleEndian.AppendUint32(cert, 1) // KEY_EXCHANGE_ALG_RSA
	blob := []byte("RSA1")
	blob = binary.LittleEndian.AppendUint32(blob, uint32(len(mod)+8))
	blob = binary.LittleEndian.AppendUint32(blob, uint32(len(mod)*8))
	blob = binary.LittleEndian.AppendUint32(blob, uint32(len(mod)-1))
	blob = binary.LittleEndian.AppendUint32(blob, uint32(key.E))
	blob = append(append(blob, mod...), make([]byte, 8)...)
	cert = binary.LittleEndian.AppendUint16(cert, 6)
	cert = binary.LittleEndian.AppendUint16(cert, uint16(len(blob)))
	cert = append(cert, blob...)
	cert = binary.LittleEndian.AppendUint16(cert, 8)
	cert = binary.LittleEndian.AppendUint16(cert, 72)
	cert = append(cert, make([]byte, 72)...)

	var sec []byte
	sec = binary.LittleEndian.AppendUint32(sec, 2) // ENCRYPTION_METHOD_128BIT
	sec = binary.LittleEndian.AppendUint32(sec, 2) // ENCRYPTION_LEVEL_CLIENT_COMPATIBLE
	sec = binary.LittleEndian.AppendUint32(sec, 32)
	sec = binary.LittleEndian.AppendUint32(sec, uint32(len(cert)))
	sec = append(append(sec, serverRandom...), cert...)

	ud := []byte{0x03, 0x0c, 8, 0, 0xeb, 0x03, 0, 0} // TS_UD_SC_NET: I/O channel 1003, no others
	ud = append(ud, 0x02, 0x0c)
	ud = binary.LittleEndian.AppendUint16(ud, uint16(len(sec)+4))
	return append(ud, sec...)
}

// TestStandardSecurityHandshake runs the client against a fake server from the server data to the
// encrypted fast-path: Security Exchange, encrypted Client Info, encrypted licensing, fast-path
// input and output.
func TestStandardSecurityHandshake(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	serverRandom := make([]byte, 32)
	_, _ = rand.Read(serverRandom)
	mcs := &fakeMCS{userData: serverUserData(&key.PublicKey, serverRandom)}
	cliConn, srvConn := net.Pipe()
	defer cliConn.Close()
	defer srvConn.Close()
	c := &Client{username: "alice", password: "lab-alice-pass", desktopWidth: 800, desktopHeight: 600, colorDepth: 16,
		mcsLayer: mcs, conn: cliConn, buffReader: bufio.NewReader(cliConn), allowStandardSecurity: true}
	c.fastPath = fastpath.New(c)

	if err := c.basicSettingsExchange(); err != nil {
		t.Fatal(err)
	}
	if err := c.channelConnection(); err != nil {
		t.Fatal(err)
	}
	if err := c.secureSettingsExchange(); err != nil {
		t.Fatal(err)
	}
	if len(mcs.sent) != 2 {
		t.Fatalf("%d PDUs sent, want Security Exchange + Client Info", len(mcs.sent))
	}

	// Security Exchange PDU: SEC_EXCHANGE_PKT | SEC_LICENSE_ENCRYPT_SC, length, encrypted random.
	ex := mcs.sent[0]
	if binary.LittleEndian.Uint16(ex) != 0x0201 || int(binary.LittleEndian.Uint32(ex[4:])) != 128+8 || len(ex) != 8+136 {
		t.Fatalf("security exchange % x", ex[:8])
	}
	m := new(big.Int).Exp(new(big.Int).SetBytes(reversed(ex[8:8+128])), key.D, key.N)
	srv := serverSide(reversed(m.FillBytes(make([]byte, 32))), serverRandom)

	// Client Info: SEC_INFO_PKT | SEC_ENCRYPT, MAC, encrypted TS_INFO_PACKET with the password.
	info := mcs.sent[1]
	if binary.LittleEndian.Uint16(info) != secInfoPkt|secEncrypt {
		t.Fatalf("client info flags %#x", binary.LittleEndian.Uint16(info))
	}
	if bytes.Contains(info, utf16le("lab-alice-pass")) {
		t.Fatal("the password went out in clear text")
	}
	plain, err := srv.open(info[4:], false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(plain, utf16le("alice")) || !bytes.Contains(plain, utf16le("lab-alice-pass")) {
		t.Fatal("client info does not carry the logon")
	}

	// Licensing: an encrypted ERROR_ALERT / STATUS_VALID_CLIENT.
	lic := []byte{0xff, 0x03, 0x10, 0x00, 0x07, 0, 0, 0, 0x02, 0, 0, 0, 0x04, 0, 0, 0}
	mcs.recv = append(mcs.recv, append(secHeader(secLicensePkt|secEncrypt|secLicenseEncrypt), srv.seal(lic)...))
	if err := c.licensing(); err != nil {
		t.Fatal(err)
	}

	// Fast-path input goes out encrypted (FASTPATH_INPUT_ENCRYPTED, MAC, RC4).
	event := pdu.NewKeyboardEvent(pdu.KBDFlagsRelease, 0x1e).Serialize()
	go func() { _ = c.SendInputEvent(event) }()
	in := make([]byte, 2+8+len(event))
	if _, err := io.ReadFull(srvConn, in); err != nil {
		t.Fatal(err)
	}
	if in[0]>>6 != fastPathEncrypted || int(in[1]) != len(in) {
		t.Fatalf("fast-path input header % x", in[:2])
	}
	if got, err := srv.open(in[2:], false); err != nil || !bytes.Equal(got, event) {
		t.Fatalf("fast-path input % x %v", got, err)
	}

	// Fast-path output from the server (FASTPATH_OUTPUT_ENCRYPTED) is decrypted.
	update := []byte{0x03, 0, 0} // FASTPATH_UPDATETYPE_SYNCHRONIZE, size 0
	body := srv.seal(update)
	go func() { _, _ = srvConn.Write(append([]byte{fastPathEncrypted << 6, byte(2 + len(body))}, body...)) }()
	u, err := c.fastPath.Receive()
	if err != nil || !bytes.Equal(u.Data, update) {
		t.Fatalf("fast-path output % x %v", u, err)
	}
}

// TestStandardSecurityNotAllowed: SSL_NOT_ALLOWED_BY_SERVER is refused without the option, retried
// once on a new connection offering PROTOCOL_RDP with it.
func TestStandardSecurityNotAllowed(t *testing.T) {
	for _, allow := range []bool{false, true} {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		offers := make(chan uint32, 2)
		go func() {
			for i := 0; ; i++ {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				hdr := make([]byte, 4)
				_, _ = io.ReadFull(conn, hdr)
				req := make([]byte, int(binary.BigEndian.Uint16(hdr[2:]))-4)
				_, _ = io.ReadFull(conn, req)
				offer := binary.LittleEndian.Uint32(req[len(req)-4:])
				offers <- offer
				typ, code := byte(3), byte(2) // RDP_NEG_FAILURE, SSL_NOT_ALLOWED_BY_SERVER
				if offer == 0 {
					typ, code = 2, 0 // RDP_NEG_RSP, PROTOCOL_RDP
				}
				_, _ = conn.Write([]byte{3, 0, 0, 19, 14, 0xd0, 0, 0, 0x12, 0x34, 0, typ, 0, 8, 0, code, 0, 0, 0})
				if offer != 0 {
					_ = conn.Close()
				}
			}
		}()
		c, err := NewClient(ln.Addr().String(), "alice", "lab-alice-pass", 800, 600, 16)
		if err != nil {
			t.Fatal(err)
		}
		c.SetUseNLA(true)
		c.SetAllowStandardSecurity(allow)
		err = c.connectionInitiation()
		_ = c.Close()
		_ = ln.Close()
		if first := <-offers; first != uint32(pdu.NegotiationProtocolSSL|pdu.NegotiationProtocolHybrid) {
			t.Fatalf("first offer %#x", first)
		}
		if !allow {
			if !errors.Is(err, ErrStandardSecurity) || !strings.Contains(err.Error(), "Allow legacy RDP security") {
				t.Fatalf("without the option: %v", err)
			}
			continue
		}
		if err != nil || !c.selectedProtocol.IsRDP() {
			t.Fatalf("with the option: %v (protocol %#x)", err, c.selectedProtocol)
		}
		if second := <-offers; second != 0 {
			t.Fatalf("retry offered %#x, want PROTOCOL_RDP", second)
		}
	}
}
