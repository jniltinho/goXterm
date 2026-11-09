// rustxterm addition (not in upstream go-rdp): Standard RDP Security (MS-RDPBCGR 5.3) — the client
// random sent RSA-encrypted to the server key, RC4 session keys (40, 56 or 128 bits) and MAC
// signatures — used only when the session allows it (SetAllowStandardSecurity), for servers whose
// Security Layer is "RDP". Ported from the Rust app (archive/rust/crates/rdp/src/legacy.rs). It
// cannot authenticate the server (the proprietary certificate is signed with a published key), so
// the caller decides trust on the server's RSA key fingerprint (SetServerKeyCheck); RC4 is weak.

package rdp

import (
	"bytes"
	"crypto/md5" // #nosec G501 -- MS-RDPBCGR 5.3.5 key derivation and MAC
	"crypto/rand"
	"crypto/rc4"  // #nosec G503 -- MS-RDPBCGR 5.3.6 session encryption
	"crypto/sha1" // #nosec G505 -- MS-RDPBCGR 5.3.5 key derivation and MAC
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	encasn1 "encoding/asn1"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"sync"

	"golang.org/x/crypto/cryptobyte"
	"golang.org/x/crypto/cryptobyte/asn1"

	"rustxterm-go/internal/rdpmit/protocol/mcs"
	"rustxterm-go/internal/rdpmit/protocol/pdu"
)

// TS_SECURITY_HEADER flags (MS-RDPBCGR 2.2.8.1.1.2.1).
const (
	secExchangePkt    = 0x0001
	secEncrypt        = 0x0008
	secInfoPkt        = 0x0040
	secLicenseEncrypt = 0x0200 // SEC_LICENSE_ENCRYPT_CS from the server, SEC_LICENSE_ENCRYPT_SC from the client
	secRedirectionPkt = 0x0400
	secSecureChecksum = 0x0800

	fastPathEncrypted = 0x2 // FASTPATH_INPUT_ENCRYPTED / FASTPATH_OUTPUT_ENCRYPTED
	keyUpdateInterval = 4096

	encryptionLevelLow  = 1
	encryptionLevelFIPS = 4
	maxModulusBytes     = 4096 / 8
)

// LegacyKeyPrefix starts a standard-security server key fingerprint ("RSA AA:BB:…"), so it never
// matches a TLS certificate fingerprint in the trust store (same as the Rust app).
const LegacyKeyPrefix = "RSA "

var (
	pad1 = bytes.Repeat([]byte{0x36}, 40)
	pad2 = bytes.Repeat([]byte{0x5c}, 48)
)

// keySize is the negotiated RC4 strength: 40/56-bit keys keep the first 64 bits with a fixed salt
// (MS-RDPBCGR 5.3.5.1).
type keySize struct {
	len  int
	salt []byte
}

var (
	key40  = keySize{8, []byte{0xd1, 0x26, 0x9e}}
	key56  = keySize{8, []byte{0xd1}}
	key128 = keySize{16, nil}
)

// fix truncates a 128-bit key to the negotiated length and salts it.
func (k keySize) fix(key []byte) []byte {
	out := append([]byte{}, key[:k.len]...)
	copy(out, k.salt)
	return out
}

// saltedHash is MD5(S + SHA(I + S + ClientRandom + ServerRandom)) (MS-RDPBCGR 5.3.5.1).
func saltedHash(s, in, clientRandom, serverRandom []byte) []byte {
	h := sha1.New() // #nosec G401
	h.Write(in)
	h.Write(s)
	h.Write(clientRandom)
	h.Write(serverRandom)
	m := md5.New() // #nosec G401
	m.Write(s)
	m.Write(h.Sum(nil))
	return m.Sum(nil)
}

// sessionKeys derives the MAC key and the client's initial encrypt and decrypt keys
// (MS-RDPBCGR 5.3.5.1), truncated and salted for 40/56 bits.
func sessionKeys(clientRandom, serverRandom []byte, size keySize) (macKey, encryptKey, decryptKey []byte) {
	pre := append(append([]byte{}, clientRandom[:24]...), serverRandom[:24]...)
	var master, blob []byte
	for _, in := range []string{"A", "BB", "CCC"} {
		master = append(master, saltedHash(pre, []byte(in), clientRandom, serverRandom)...)
	}
	for _, in := range []string{"X", "YY", "ZZZ"} {
		blob = append(blob, saltedHash(master, []byte(in), clientRandom, serverRandom)...)
	}
	final := func(k []byte) []byte {
		m := md5.New() // #nosec G401
		m.Write(k)
		m.Write(clientRandom)
		m.Write(serverRandom)
		return m.Sum(nil)
	}
	return size.fix(blob[:16]), size.fix(final(blob[32:48])), size.fix(final(blob[16:32]))
}

// macSignature is the 8-byte MAC (MS-RDPBCGR 5.3.6.1); count is the salted-MAC packet count
// (5.3.6.1.1), nil for the plain MAC.
func macSignature(key, data, count []byte) []byte {
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(len(data))) // #nosec G115
	h := sha1.New()                                        // #nosec G401
	h.Write(key)
	h.Write(pad1)
	h.Write(n[:])
	h.Write(data)
	h.Write(count)
	m := md5.New() // #nosec G401
	m.Write(key)
	m.Write(pad2)
	m.Write(h.Sum(nil))
	return m.Sum(nil)[:8]
}

// rc4Stream is one direction's RC4 state, refreshed every 4096 packets (MS-RDPBCGR 5.3.7.1).
type rc4Stream struct {
	size         keySize
	initial, key []byte
	c            *rc4.Cipher
	used         int
	total        uint32 // packets so far, for the salted MAC
}

func newRC4Stream(key []byte, size keySize) *rc4Stream {
	c, _ := rc4.NewCipher(key) // #nosec G405
	return &rc4Stream{size: size, initial: key, key: key, c: c}
}

// xor encrypts or decrypts one packet and returns its count.
func (s *rc4Stream) xor(b []byte) uint32 {
	if s.used == keyUpdateInterval {
		h := sha1.New() // #nosec G401
		h.Write(s.initial)
		h.Write(pad1)
		h.Write(s.key)
		m := md5.New() // #nosec G401
		m.Write(s.initial)
		m.Write(pad2)
		m.Write(h.Sum(nil))
		k := m.Sum(nil)[:s.size.len]
		c, _ := rc4.NewCipher(k) // #nosec G405
		c.XORKeyStream(k, k)
		s.key = s.size.fix(k)
		s.c, _ = rc4.NewCipher(s.key) // #nosec G405
		s.used = 0
	}
	s.c.XORKeyStream(b, b)
	s.used++
	n := s.total
	s.total++
	return n
}

// stdSecurity wraps the MCS layer once the keys are set: every slow-path PDU sent gets
// SEC_ENCRYPT, a MAC and RC4; every PDU received loses its security header (and is decrypted).
type stdSecurity struct {
	mcs.MCSLayer

	mu       sync.Mutex // encrypt and write together, so the server's RC4 stream stays in step
	level    uint32
	macKey   []byte
	enc, dec *rc4Stream

	licenseEncrypt bool // the server takes encrypted licensing PDUs (SEC_LICENSE_ENCRYPT_CS)
}

func newStdSecurity(inner mcs.MCSLayer, level uint32, size keySize, clientRandom, serverRandom []byte) *stdSecurity {
	mac, enc, dec := sessionKeys(clientRandom, serverRandom, size)
	return &stdSecurity{MCSLayer: inner, level: level, macKey: mac, enc: newRC4Stream(enc, size), dec: newRC4Stream(dec, size)}
}

// seal returns MAC + encrypted data; the caller holds mu until the result is written.
func (s *stdSecurity) seal(data []byte) []byte {
	out := append(macSignature(s.macKey, data, nil), data...)
	s.enc.xor(out[8:])
	return out
}

// open decrypts MAC + data in place and checks the MAC.
func (s *stdSecurity) open(data []byte, salted bool) ([]byte, error) {
	if len(data) < 8 {
		return nil, errors.New("legacy RDP security: encrypted PDU shorter than its signature")
	}
	body := data[8:]
	n := s.dec.xor(body)
	var count []byte
	if salted {
		count = binary.LittleEndian.AppendUint32(nil, n)
	}
	if subtle.ConstantTimeCompare(macSignature(s.macKey, body, count), data[:8]) != 1 {
		return nil, errors.New("legacy RDP security: invalid packet signature")
	}
	return body, nil
}

// requireEncrypted: clear traffic is legal only at level Low (server-to-client unencrypted).
func (s *stdSecurity) requireEncrypted() error {
	if s.level == encryptionLevelLow {
		return nil
	}
	return fmt.Errorf("legacy RDP security: unencrypted PDU at encryption level %d", s.level)
}

// openFastPath is fastpath.Protocol.Decrypt.
func (s *stdSecurity) openFastPath(encrypted, salted bool, data []byte) ([]byte, error) {
	if !encrypted {
		return data, s.requireEncrypted()
	}
	return s.open(data, salted)
}

func secHeader(flags uint16) []byte { return []byte{byte(flags), byte(flags >> 8), 0, 0} }

// Send encrypts a slow-path PDU (SEC_ENCRYPT).
func (s *stdSecurity) Send(userID, channelID uint16, data []byte) error {
	return s.send(userID, channelID, 0, data)
}

func (s *stdSecurity) send(userID, channelID, flags uint16, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.MCSLayer.Send(userID, channelID, append(secHeader(flags|secEncrypt), s.seal(data)...))
}

// sendLicense sends a licensing PDU (without its basic header): in clear until the server offers
// to process encrypted ones.
func (s *stdSecurity) sendLicense(userID, channelID uint16, data []byte) error {
	if s.licenseEncrypt {
		return s.send(userID, channelID, secLicensePkt|secLicenseEncrypt, data)
	}
	return s.MCSLayer.Send(userID, channelID, append(secHeader(secLicensePkt), data...))
}

// Receive strips the security header, decrypting when SEC_ENCRYPT is set. Licensing PDUs keep a
// plain basic header, since their parser reads the SEC_LICENSE_PKT flag.
func (s *stdSecurity) Receive() (uint16, io.Reader, error) {
	ch, wire, err := s.MCSLayer.Receive()
	if err != nil {
		return ch, wire, err
	}
	data, err := io.ReadAll(wire)
	if err != nil {
		return ch, nil, err
	}
	if len(data) < 4 {
		return ch, nil, errors.New("legacy RDP security: short security header")
	}
	flags := binary.LittleEndian.Uint16(data)
	if flags&secRedirectionPkt != 0 {
		return ch, nil, errors.New("server redirection is not supported with legacy RDP security")
	}
	license := flags&secLicensePkt != 0
	if license && flags&secLicenseEncrypt != 0 {
		s.licenseEncrypt = true
	}
	body := data[4:]
	switch {
	case flags&secEncrypt != 0:
		if body, err = s.open(body, flags&secSecureChecksum != 0); err != nil {
			return ch, nil, err
		}
	case !license:
		if err := s.requireEncrypted(); err != nil {
			return ch, nil, err
		}
	}
	if license {
		return ch, bytes.NewReader(append(secHeader(secLicensePkt), body...)), nil
	}
	return ch, bytes.NewReader(body), nil
}

// serverKey is the server's RSA public key, big-endian without leading zeros.
type serverKey struct {
	modulus, exponent []byte
}

// serverPublicKey reads the key from a proprietary certificate or from the leaf of an X.509 chain
// (MS-RDPBCGR 2.2.1.4.3.1) and refuses odd sizes: 512–4096-bit odd modulus, odd 32-bit exponent ≥ 3
// (exponent 1 would expose the client random).
func serverPublicKey(cert *pdu.ServerCertificate) (*serverKey, error) {
	if cert == nil {
		return nil, errors.New("the server sent no certificate")
	}
	var k serverKey
	if p := cert.ProprietaryCert; p != nil {
		m := p.PublicKeyBlob.Modulus // little-endian, then 8 zero bytes
		k.modulus = reversed(m[:max(len(m)-8, 0)])
		k.exponent = binary.BigEndian.AppendUint32(nil, p.PublicKeyBlob.PubExp)
	} else {
		n, e, err := x509ChainKey(cert.X509Cert)
		if err != nil {
			return nil, err
		}
		k.modulus, k.exponent = n.Bytes(), big.NewInt(int64(e)).Bytes()
	}
	k.modulus, k.exponent = trimZeros(k.modulus), trimZeros(k.exponent)
	odd := func(b []byte) bool { return len(b) > 0 && b[len(b)-1]&1 == 1 }
	if len(k.modulus) < 64 || len(k.modulus) > maxModulusBytes || !odd(k.modulus) {
		return nil, fmt.Errorf("unsupported server RSA key (%d bits)", len(k.modulus)*8)
	}
	if len(k.exponent) > 4 || !odd(k.exponent) || bytes.Equal(k.exponent, []byte{1}) {
		return nil, errors.New("unsupported server RSA exponent")
	}
	return &k, nil
}

func x509ChainKey(chain []byte) (*big.Int, int, error) {
	r := bytes.NewReader(chain)
	var count uint32
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil || count == 0 || count > 16 {
		return nil, 0, errors.New("malformed X.509 server certificate chain")
	}
	var leaf []byte
	for range count {
		var n uint32
		if err := binary.Read(r, binary.LittleEndian, &n); err != nil || int64(n) > int64(r.Len()) {
			return nil, 0, errors.New("malformed X.509 server certificate chain")
		}
		leaf = make([]byte, n)
		_, _ = io.ReadFull(r, leaf)
	}
	// Terminal Services license certificates use OIDs crypto/x509 does not take, so only the
	// subjectPublicKeyInfo is read.
	in := cryptobyte.String(leaf)
	var cert, tbs, spki cryptobyte.String
	var bits encasn1.BitString
	if !in.ReadASN1(&cert, asn1.SEQUENCE) || !cert.ReadASN1(&tbs, asn1.SEQUENCE) ||
		!tbs.SkipOptionalASN1(asn1.Tag(0).Constructed().ContextSpecific()) ||
		!tbs.SkipASN1(asn1.INTEGER) || !tbs.SkipASN1(asn1.SEQUENCE) || !tbs.SkipASN1(asn1.SEQUENCE) ||
		!tbs.SkipASN1(asn1.SEQUENCE) || !tbs.SkipASN1(asn1.SEQUENCE) || !tbs.ReadASN1(&spki, asn1.SEQUENCE) ||
		!spki.SkipASN1(asn1.SEQUENCE) || !spki.ReadASN1BitString(&bits) {
		return nil, 0, errors.New("malformed X.509 server certificate")
	}
	k, err := x509.ParsePKCS1PublicKey(bits.RightAlign())
	if err != nil {
		return nil, 0, fmt.Errorf("server key is not RSA: %w", err)
	}
	return k.N, k.E, nil
}

func reversed(b []byte) []byte {
	out := make([]byte, len(b))
	for i, v := range b {
		out[len(b)-1-i] = v
	}
	return out
}

func trimZeros(b []byte) []byte {
	for len(b) > 0 && b[0] == 0 {
		b = b[1:]
	}
	return b
}

// fingerprint is "RSA AA:BB:…": SHA-256 over the length-prefixed big-endian modulus and exponent
// (the Rust app's format, so keys trusted there stay trusted).
func (k *serverKey) fingerprint() string {
	h := sha256.New()
	for _, b := range [][]byte{k.modulus, k.exponent} {
		h.Write(binary.BigEndian.AppendUint32(nil, uint32(len(b)))) // #nosec G115 -- ≤ 512 bytes
		h.Write(b)
	}
	parts := make([]string, 0, sha256.Size)
	for _, b := range h.Sum(nil) {
		parts = append(parts, fmt.Sprintf("%02X", b))
	}
	return LegacyKeyPrefix + strings.Join(parts, ":")
}

// encryptClientRandom is the raw (unpadded) RSA of MS-RDPBCGR 5.3.4.1, little-endian, modulus
// width followed by 8 zero bytes.
func (k *serverKey) encryptClientRandom(random []byte) []byte {
	m := new(big.Int).SetBytes(reversed(random))
	c := new(big.Int).Exp(m, new(big.Int).SetBytes(k.exponent), new(big.Int).SetBytes(k.modulus))
	return append(reversed(c.FillBytes(make([]byte, len(k.modulus)))), make([]byte, 8)...)
}

// negotiated checks the server's choice: FIPS and "no encryption" (the logon in clear) are refused.
func negotiated(sd *pdu.ServerSecurityData) (keySize, error) {
	switch {
	case sd == nil || sd.EncryptionMethod == 0 || sd.EncryptionLevel == 0:
		return keySize{}, errors.New("the server offers no encryption (level None); credentials would travel in clear")
	case sd.EncryptionLevel == encryptionLevelFIPS || sd.EncryptionMethod&pdu.FIPSEncryptionFlag != 0:
		return keySize{}, errors.New("the server requires FIPS encryption, which legacy RDP security does not support")
	case len(sd.ServerRandom) != 32:
		return keySize{}, errors.New("server random missing")
	}
	switch sd.EncryptionMethod {
	case pdu.EncryptionFlag128Bit:
		return key128, nil
	case pdu.EncryptionFlag56Bit:
		return key56, nil
	case pdu.EncryptionFlag40Bit:
		return key40, nil
	}
	return keySize{}, fmt.Errorf("unexpected server encryption method %#x", sd.EncryptionMethod)
}

// securityExchange checks the server key (SetServerKeyCheck: trust on the fingerprint), sends the
// encrypted client random (Security Exchange PDU, MS-RDPBCGR 2.2.1.10) and switches the MCS layer
// and fast-path to encryption.
func (c *Client) securityExchange() error {
	size, err := negotiated(c.serverSecurity)
	if err != nil {
		return err
	}
	key, err := serverPublicKey(c.serverSecurity.ServerCertificate)
	if err != nil {
		return err
	}
	if c.serverKeyCheck != nil {
		if err := c.serverKeyCheck(key.fingerprint()); err != nil {
			return err
		}
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	enc := key.encryptClientRandom(random)
	body := binary.LittleEndian.AppendUint32(secHeader(secExchangePkt|secLicenseEncrypt), uint32(len(enc))) // #nosec G115
	if err := c.mcsLayer.Send(c.userID, c.channelIDMap["global"], append(body, enc...)); err != nil {
		return err
	}
	c.stdSec = newStdSecurity(c.mcsLayer, c.serverSecurity.EncryptionLevel, size, random, c.serverSecurity.ServerRandom)
	c.mcsLayer = c.stdSec
	c.fastPath.Decrypt = c.stdSec.openFastPath
	return nil
}
