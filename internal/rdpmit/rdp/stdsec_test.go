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

// rustRandoms are the Rust app's test randoms: client 00..1f, server 80..9f.
func rustRandoms() (client, server []byte) {
	for i := range 32 {
		client, server = append(client, byte(i)), append(server, byte(0x80+i))
	}
	return
}

// TestSessionKeysFreeRDP: keys, MACs and ciphertexts (packets 0, 4095, 4096 — first after the key
// update — and 4097) for 40, 56 and 128 bits, as printed by the Rust app's FreeRDP 3.10.3 oracle
// (archive/rust/crates/rdp/src/legacy/tests.rs).
func TestSessionKeysFreeRDP(t *testing.T) {
	data := unhex(t, "a0a1a2a3a4a5a6a7a8a9aaabacadaeaf")
	for _, v := range []struct {
		size keySize
		want [9]string
	}{
		{key40, [9]string{"d1269e9347180b6a", "d1269ecb8ddc9011", "d1269e59314b292d", "275924ad7d2abe43", "f66d6a05a00705bb",
			"e767897b8b83bdfaf04303c71fdbcbec", "bc9be65d451398a081545b1e327742dd", "c65ce67cef3070978bffa3c88816b2b9", "3dcf52b5bfc5a8b739b48fd44f68f04d"}},
		{key56, [9]string{"d1a9be9347180b6a", "d18177cb8ddc9011", "d174ee59314b292d", "274cd5edb7595ab0", "da74414acfecb388",
			"0bb8b448cc5b7cb457bcbb1cc907697c", "6cadb14168912ee9ab2475f76fb49a48", "6db47eb16cbfff073c495388c0f1c296", "8f2657a4c0ec5012883310a66500cb4e"}},
		{key128, [9]string{"67a9be9347180b6a49918713db69f2c5", "818177cb8ddc9011780552cb3d140abb", "4574ee59314b292dc2fbd69d00aa7331", "f6d3866465dac408", "a744b8e56351ce52",
			"1ffa6326e663ff64cd2f74ee6b86ce27", "57fbf70e4c7d719e73ae07cada6f6e13", "08b1c2baeae502f22f061895faa409ab", "80fda948914b77571460217e5561d73f"}},
	} {
		cr, sr := rustRandoms()
		mac, enc, dec := sessionKeys(cr, sr, v.size)
		got := []string{hex.EncodeToString(mac), hex.EncodeToString(enc), hex.EncodeToString(dec),
			hex.EncodeToString(macSignature(mac, data, nil)), hex.EncodeToString(macSignature(mac, data, []byte{7, 0, 0, 0}))}
		s := newRC4Stream(enc, v.size)
		for packet := range uint32(4098) {
			b := append([]byte{}, data...)
			if n := s.xor(b); n != packet {
				t.Fatalf("packet count %d, want %d", n, packet)
			}
			if packet == 0 || packet >= 4095 {
				got = append(got, hex.EncodeToString(b))
			}
		}
		for i, w := range v.want {
			if got[i] != w {
				t.Fatalf("%d-byte keys, value %d: %s, want %s", v.size.len, i, got[i], w)
			}
		}
	}
}

// rustKey is the Rust app's 544-bit test key (exponent 65537).
func rustKey(t *testing.T) *serverKey {
	return &serverKey{modulus: unhex(t, "C3C4E2C2B1B9C5F2D3E1C0B5A4F7E6D9C8B7A6F5E4D3C2B1A0F9E8D7C6B5A49"+
		"384736251403F2E1D0C1B2A3948576A5B4C3D2E1F0A9B8C7D6E5F4031425364758697A8B9"), exponent: []byte{1, 0, 1}}
}

// TestEncryptClientRandom: FreeRDP's RSA output for the Rust test key (modulus width plus 8 zeros),
// and MS-RDPBCGR 4.8's 512-bit key pair, whose private exponent turns the result back.
func TestEncryptClientRandom(t *testing.T) {
	cr, _ := rustRandoms()
	want := append(unhex(t, "2bff714d9555fcc63bf5007666be307f445b5bbe6ac05d3340a81112d8559cb0084858a7344f5485f972d9b8df2d6f0a66"+
		"088aef3177e7b8e54e02cb0bc7e54dec4c8794"), make([]byte, 8)...)
	if got := rustKey(t).encryptClientRandom(cr); !bytes.Equal(got, want) {
		t.Fatalf("RSA % x", got)
	}

	mod := unhex(t, "37a870fe9ab9a854cb9879447ab9eb3806ea26a147ea19705df352887021b59e"+
		"50b4e1f51ad82d514d1aad797c8946b0cc667402d8285d9dd7cafc600f38f9b3")
	priv := unhex(t, "c107e7d4d3388d36f59e8b960d5565082825a32ec768d644852d32f672a89bba"+
		"5e8282f05c0ceb6b126aa74515ce41e003e5e66ddffd58610b07a47bb3f37194")
	k := &serverKey{modulus: reversed(mod), exponent: []byte{1, 0, 1}}
	random := unhex(t, specClientRandom)
	enc := k.encryptClientRandom(random)
	if len(enc) != 72 || !bytes.Equal(enc[64:], make([]byte, 8)) {
		t.Fatalf("encrypted random %d bytes, tail % x", len(enc), enc[64:])
	}
	m := new(big.Int).Exp(new(big.Int).SetBytes(reversed(enc[:64])), new(big.Int).SetBytes(reversed(priv)), new(big.Int).SetBytes(k.modulus))
	if got := reversed(m.FillBytes(make([]byte, 32))); !bytes.Equal(got, random) {
		t.Fatalf("decrypted % x", got)
	}
}

func proprietaryCert(modulusBE []byte, exponent uint32) *pdu.ServerCertificate {
	return &pdu.ServerCertificate{ProprietaryCert: &pdu.ServerProprietaryCertificate{PublicKeyBlob: pdu.RSAPublicKey{
		PubExp: exponent, Modulus: append(reversed(modulusBE), make([]byte, 8)...)}}}
}

// TestServerKeyChecks: the proprietary key and its fingerprint; odd sizes and exponents refused;
// the fingerprint binds the modulus/exponent split.
func TestServerKeyChecks(t *testing.T) {
	k, err := serverPublicKey(proprietaryCert(rustKey(t).modulus, 65537))
	if err != nil || !bytes.Equal(k.modulus, rustKey(t).modulus) || !bytes.Equal(k.exponent, []byte{1, 0, 1}) {
		t.Fatalf("key %x %x %v", k.modulus, k.exponent, err)
	}
	if fp := k.fingerprint(); !strings.HasPrefix(fp, LegacyKeyPrefix) || len(fp) != len(LegacyKeyPrefix)+95 {
		t.Fatalf("fingerprint %q", fp)
	}
	big := bytes.Repeat([]byte{0xff}, 513)
	for _, c := range []*pdu.ServerCertificate{nil, proprietaryCert(big, 65537), proprietaryCert(rustKey(t).modulus, 1),
		proprietaryCert(rustKey(t).modulus, 2), proprietaryCert(rustKey(t).modulus, 65536), {X509Cert: []byte{9, 0, 0, 0, 1, 2}}} {
		if _, err := serverPublicKey(c); err == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
	other := &serverKey{modulus: append(append([]byte{}, rustKey(t).modulus...), 1, 0), exponent: []byte{1}}
	if other.fingerprint() == rustKey(t).fingerprint() {
		t.Fatal("fingerprint does not bind the modulus/exponent split")
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
	if size, err := negotiated(sud.ServerSecurityData); err != nil || size.len != 16 {
		t.Fatal(err)
	}
	if sud.ServerNetworkData == nil || sud.ServerNetworkData.MCSChannelId != 1003 {
		t.Fatal("the blocks after the security data were not parsed")
	}
	k, err := serverPublicKey(sud.ServerSecurityData.ServerCertificate)
	if err != nil {
		t.Fatal(err)
	}
	if len(k.modulus) != 64 || k.modulus[63] != 0xcb || !bytes.Equal(k.exponent, []byte{1, 0, 1}) {
		t.Fatalf("key %x %x", k.modulus, k.exponent)
	}
	random := make([]byte, 32)
	for _, sd := range []*pdu.ServerSecurityData{
		nil, {EncryptionMethod: 0, EncryptionLevel: 0},
		{EncryptionMethod: pdu.FIPSEncryptionFlag, EncryptionLevel: 4, ServerRandom: random},
		{EncryptionMethod: pdu.EncryptionFlag56Bit, EncryptionLevel: 3},
	} {
		if _, err := negotiated(sd); err == nil {
			t.Fatalf("accepted %+v", sd)
		}
	}
	if size, err := negotiated(&pdu.ServerSecurityData{EncryptionMethod: pdu.EncryptionFlag40Bit, EncryptionLevel: 2, ServerRandom: random}); err != nil || size.len != 8 {
		t.Fatalf("40-bit: %v", err)
	}
}

// pair is the client's state over a fake MCS layer and the server's mirror (its encrypt key is the
// client's decrypt key), 128-bit.
func pair(level uint32) (*stdSecurity, *stdSecurity, *fakeMCS) {
	cr, sr := rustRandoms()
	f := &fakeMCS{}
	return newStdSecurity(f, level, key128, cr, sr), serverSide(level, key128, cr, sr), f
}

func serverSide(level uint32, size keySize, clientRandom, serverRandom []byte) *stdSecurity {
	mac, enc, dec := sessionKeys(clientRandom, serverRandom, size)
	return &stdSecurity{level: level, macKey: mac, enc: newRC4Stream(dec, size), dec: newRC4Stream(enc, size)}
}

// serverOpen is what the server does with a sealed slow-path PDU: flags and plaintext.
func serverOpen(t *testing.T, srv *stdSecurity, pdu []byte) (uint16, []byte) {
	t.Helper()
	body, err := srv.open(append([]byte{}, pdu[4:]...), false)
	if err != nil {
		t.Fatal(err)
	}
	return binary.LittleEndian.Uint16(pdu), body
}

// serverSeal is what the server sends: an encrypted PDU with flags.
func serverSeal(srv *stdSecurity, flags uint16, body []byte) []byte {
	return append(secHeader(flags|secEncrypt), srv.seal(body)...)
}

func receive(t *testing.T, c *stdSecurity, f *fakeMCS, pdu []byte) ([]byte, error) {
	t.Helper()
	f.recv = append(f.recv, pdu)
	_, r, err := c.Receive()
	if err != nil {
		return nil, err
	}
	b, _ := io.ReadAll(r)
	return b, nil
}

// TestStdSecuritySlowPath ports the Rust app's legacy tests: headers kept, licensing in clear until
// the server offers encryption, licensing PDUs handed on with a plain basic header, salted MACs,
// bad signatures, redirection and clear traffic above level Low refused, key updates both ways.
func TestStdSecuritySlowPath(t *testing.T) {
	c, srv, f := pair(2) // ENCRYPTION_LEVEL_CLIENT_COMPATIBLE
	if err := c.Send(1007, 1003, []byte("share-data")); err != nil {
		t.Fatal(err)
	}
	_ = c.send(1007, 1003, secInfoPkt, []byte{1, 2, 3})
	_ = c.sendLicense(1007, 1003, []byte{9})
	if fl, b := serverOpen(t, srv, f.sent[0]); fl != secEncrypt || string(b) != "share-data" {
		t.Fatalf("data %#x %q", fl, b)
	}
	if fl, b := serverOpen(t, srv, f.sent[1]); fl != secInfoPkt|secEncrypt || !bytes.Equal(b, []byte{1, 2, 3}) {
		t.Fatalf("info %#x % x", fl, b)
	}
	if !bytes.Equal(f.sent[2], []byte{0x80, 0, 0, 0, 9}) {
		t.Fatalf("licensing must go in clear until the server offers encryption: % x", f.sent[2])
	}

	if b, err := receive(t, c, f, serverSeal(srv, 0, []byte("demand-active"))); err != nil || string(b) != "demand-active" {
		t.Fatalf("inbound data %q %v", b, err)
	}
	if b, err := receive(t, c, f, serverSeal(srv, secLicensePkt|secLicenseEncrypt, []byte("lic"))); err != nil ||
		!bytes.Equal(b, []byte{0x80, 0, 0, 0, 'l', 'i', 'c'}) {
		t.Fatalf("inbound licensing % x %v", b, err)
	}
	_ = c.sendLicense(1007, 1003, []byte{9})
	if fl, b := serverOpen(t, srv, f.sent[3]); fl != secLicensePkt|secEncrypt|secLicenseEncrypt || !bytes.Equal(b, []byte{9}) {
		t.Fatalf("encrypted licensing %#x % x", fl, b)
	}

	// Salted MACs use the packet count (3 packets decrypted so far).
	for i := range uint32(3) {
		body := []byte{byte(i), 5, 5}
		sig := macSignature(srv.macKey, body, binary.LittleEndian.AppendUint32(nil, 2+i))
		srv.enc.xor(body)
		if b, err := receive(t, c, f, append(append(secHeader(secEncrypt|secSecureChecksum), sig...), body...)); err != nil || b[0] != byte(i) {
			t.Fatalf("salted %d: % x %v", i, b, err)
		}
	}

	bad := serverSeal(srv, 0, []byte("payload"))
	bad[len(bad)-1] ^= 1
	if _, err := receive(t, c, f, bad); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("bad signature: %v", err)
	}
	if _, err := receive(t, c, f, []byte{0, 4, 0, 0, 'r'}); err == nil {
		t.Fatal("redirection accepted")
	}
	if _, err := receive(t, c, f, []byte{0, 0, 0, 0, 'x'}); err == nil {
		t.Fatal("clear PDU accepted above level Low")
	}
	if b, err := receive(t, c, f, []byte{0x80, 0, 0, 0, 'l'}); err != nil || !bytes.Equal(b, []byte{0x80, 0, 0, 0, 'l'}) {
		t.Fatalf("clear licensing % x %v", b, err)
	}
	if _, err := c.openFastPath(false, false, []byte("bitmap")); err == nil {
		t.Fatal("clear fast-path accepted above level Low")
	}
	if _, err := c.openFastPath(true, false, []byte{1, 2, 3}); err == nil {
		t.Fatal("truncated fast-path signature accepted")
	}

	low, _, lf := pair(1) // ENCRYPTION_LEVEL_LOW: server-to-client in clear
	if b, err := receive(t, low, lf, []byte{0, 0, 0, 0, 'x'}); err != nil || string(b) != "x" {
		t.Fatalf("level Low clear PDU % x %v", b, err)
	}
	if b, err := low.openFastPath(false, false, []byte("bitmap")); err != nil || string(b) != "bitmap" {
		t.Fatalf("level Low clear fast-path %v", err)
	}

	cli, srv2, _ := pair(2)
	for i := range 2*keyUpdateInterval + 3 { // crosses two key updates
		msg := []byte{byte(i), byte(i >> 8), 'x', 'y'}
		if got, err := srv2.open(cli.seal(msg), false); err != nil || !bytes.Equal(got, msg) {
			t.Fatalf("packet %d: % x %v", i, got, err)
		}
	}
}

// fakeMCS is the server side of the MCS layer for the handshake test.
type fakeMCS struct {
	request  []byte
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
func (f *fakeMCS) Connect(req []byte) (io.Reader, error) {
	f.request = req
	return bytes.NewReader(f.userData), nil
}
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
	var checked string
	c.SetServerKeyCheck(func(fp string) error {
		if len(mcs.sent) != 0 {
			t.Error("the key was checked after the client sent something")
		}
		checked = fp
		return nil
	})

	if err := c.basicSettingsExchange(); err != nil {
		t.Fatal(err)
	}
	// TS_UD_CS_SEC offers 40, 56 and 128-bit RC4.
	if !bytes.Contains(mcs.request, []byte{0x02, 0xc0, 0x0c, 0x00, 0x0b, 0, 0, 0}) {
		t.Fatal("client security data does not offer RC4 40/56/128")
	}
	if err := c.channelConnection(); err != nil {
		t.Fatal(err)
	}
	if err := c.secureSettingsExchange(); err != nil {
		t.Fatal(err)
	}
	want := (&serverKey{modulus: key.N.Bytes(), exponent: big.NewInt(int64(key.E)).Bytes()}).fingerprint()
	if checked != want {
		t.Fatalf("checked %q, want %q", checked, want)
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
	srv := serverSide(2, key128, reversed(m.FillBytes(make([]byte, 32))), serverRandom)

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

// TestServerKeyRefused: a refused server key stops the connection before the Security Exchange.
func TestServerKeyRefused(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	mcs := &fakeMCS{userData: serverUserData(&key.PublicKey, make([]byte, 32))}
	c := &Client{username: "alice", password: "lab-alice-pass", mcsLayer: mcs, allowStandardSecurity: true}
	c.SetServerKeyCheck(func(string) error { return errors.New("not trusted") })
	if err := c.basicSettingsExchange(); err != nil {
		t.Fatal(err)
	}
	if err := c.secureSettingsExchange(); err == nil || len(mcs.sent) != 0 {
		t.Fatalf("err %v, %d PDUs sent", err, len(mcs.sent))
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
