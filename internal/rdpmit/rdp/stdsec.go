// rustxterm addition (not in upstream go-rdp): Standard RDP Security (MS-RDPBCGR 5.3) — the client
// random sent RSA-encrypted to the server key, RC4 session keys and MAC signatures — used only when
// the session allows it (SetAllowStandardSecurity), for servers whose Security Layer is "RDP".
// It has no server authentication and RC4 is weak; the option says so.

package rdp

import (
	"bytes"
	"crypto/md5" // #nosec G501 -- MS-RDPBCGR 5.3.5 key derivation and MAC
	"crypto/rand"
	"crypto/rc4"  // #nosec G503 -- MS-RDPBCGR 5.3.6 session encryption
	"crypto/sha1" // #nosec G505 -- MS-RDPBCGR 5.3.5 key derivation and MAC
	"crypto/subtle"
	"crypto/x509"
	encasn1 "encoding/asn1"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
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
	secSecureChecksum = 0x0800

	fastPathEncrypted = 0x2 // FASTPATH_INPUT_ENCRYPTED
	keyUpdateInterval = 4096
)

var (
	pad1 = bytes.Repeat([]byte{0x36}, 40)
	pad2 = bytes.Repeat([]byte{0x5c}, 48)
)

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

// sessionKeys derives the 128-bit MAC key and the client's initial encrypt and decrypt keys
// (MS-RDPBCGR 5.3.5.1).
func sessionKeys(clientRandom, serverRandom []byte) (macKey, encryptKey, decryptKey []byte) {
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
	return blob[:16], final(blob[32:48]), final(blob[16:32])
}

// updateKey is the session key update done every 4096 packets (MS-RDPBCGR 5.3.7.1).
func updateKey(initial, current []byte) []byte {
	h := sha1.New() // #nosec G401
	h.Write(initial)
	h.Write(pad1)
	h.Write(current)
	m := md5.New() // #nosec G401
	m.Write(initial)
	m.Write(pad2)
	m.Write(h.Sum(nil))
	k := m.Sum(nil)
	c, _ := rc4.NewCipher(k) // #nosec G405
	c.XORKeyStream(k, k)
	return k
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

// rc4Stream is one direction's RC4 state with its key update.
type rc4Stream struct {
	initial, key []byte
	c            *rc4.Cipher
	used         int
}

func newRC4Stream(key []byte) *rc4Stream {
	c, _ := rc4.NewCipher(key) // #nosec G405
	return &rc4Stream{initial: key, key: key, c: c}
}

func (s *rc4Stream) xor(b []byte) {
	if s.used == keyUpdateInterval {
		s.key = updateKey(s.initial, s.key)
		s.c, _ = rc4.NewCipher(s.key) // #nosec G405
		s.used = 0
	}
	s.c.XORKeyStream(b, b)
	s.used++
}

// stdSecurity wraps the MCS layer once the keys are set: every slow-path PDU sent gets
// SEC_ENCRYPT, a MAC and RC4; every PDU received loses its security header (and is decrypted).
type stdSecurity struct {
	mcs.MCSLayer

	mu       sync.Mutex // encrypt and write together, so the server's RC4 stream stays in step
	macKey   []byte
	enc, dec *rc4Stream
	decCount uint32 // packets decrypted, for salted MACs

	licenseEncrypt bool // the server takes encrypted licensing PDUs (SEC_LICENSE_ENCRYPT_CS)
}

func newStdSecurity(inner mcs.MCSLayer, clientRandom, serverRandom []byte) *stdSecurity {
	mac, enc, dec := sessionKeys(clientRandom, serverRandom)
	return &stdSecurity{MCSLayer: inner, macKey: mac, enc: newRC4Stream(enc), dec: newRC4Stream(dec)}
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
		return nil, errors.New("standard RDP security: encrypted PDU shorter than its MAC")
	}
	body := data[8:]
	s.dec.xor(body)
	var count []byte
	if salted {
		count = binary.LittleEndian.AppendUint32(nil, s.decCount)
	}
	s.decCount++
	if subtle.ConstantTimeCompare(macSignature(s.macKey, body, count), data[:8]) != 1 {
		return nil, errors.New("standard RDP security: MAC signature mismatch")
	}
	return body, nil
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

// Receive strips the security header, decrypting when SEC_ENCRYPT is set. Licensing PDUs keep a
// basic header (without SEC_ENCRYPT), since their parser reads the SEC_LICENSE_PKT flag.
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
		return ch, nil, errors.New("standard RDP security: PDU shorter than its security header")
	}
	flags := binary.LittleEndian.Uint16(data)
	body := data[4:]
	if flags&secEncrypt != 0 {
		if body, err = s.open(body, flags&secSecureChecksum != 0); err != nil {
			return ch, nil, err
		}
	}
	if flags&secLicensePkt != 0 {
		s.licenseEncrypt = flags&secLicenseEncrypt != 0
		return ch, bytes.NewReader(append(secHeader(flags&^(secEncrypt|secSecureChecksum)), body...)), nil
	}
	return ch, bytes.NewReader(body), nil
}

// serverKey is the server's RSA public key from its certificate.
type serverKey struct {
	n    *big.Int
	e    int
	size int // modulus length in bytes; the encrypted client random has this length plus 8 zeros
}

// serverPublicKey reads the key from a proprietary certificate (its signature uses Microsoft's
// well-known, published Terminal Services key, so checking it would prove nothing) or from the leaf
// of an X.509 chain (MS-RDPBCGR 2.2.1.4.3.1). Standard RDP security cannot authenticate the server.
func serverPublicKey(cert *pdu.ServerCertificate) (*serverKey, error) {
	if cert == nil {
		return nil, errors.New("the server sent no certificate")
	}
	if p := cert.ProprietaryCert; p != nil {
		k := p.PublicKeyBlob
		if k.Magic != 0x31415352 || k.KeyLen <= 8 || int(k.KeyLen) > len(k.Modulus) || k.PubExp == 0 {
			return nil, errors.New("malformed proprietary server certificate")
		}
		size := int(k.KeyLen) - 8
		return &serverKey{n: new(big.Int).SetBytes(reversed(k.Modulus[:size])), e: int(k.PubExp), size: size}, nil
	}
	return x509ChainKey(cert.X509Cert)
}

func x509ChainKey(chain []byte) (*serverKey, error) {
	r := bytes.NewReader(chain)
	var count uint32
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil || count == 0 || count > 16 {
		return nil, errors.New("malformed X.509 server certificate chain")
	}
	var leaf []byte
	for range count {
		var n uint32
		if err := binary.Read(r, binary.LittleEndian, &n); err != nil || int64(n) > int64(r.Len()) {
			return nil, errors.New("malformed X.509 server certificate chain")
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
		return nil, errors.New("malformed X.509 server certificate")
	}
	k, err := x509.ParsePKCS1PublicKey(bits.RightAlign())
	if err != nil {
		return nil, fmt.Errorf("X.509 server certificate: %w", err)
	}
	return &serverKey{n: k.N, e: k.E, size: (k.N.BitLen() + 7) / 8}, nil
}

func reversed(b []byte) []byte {
	out := make([]byte, len(b))
	for i, v := range b {
		out[len(b)-1-i] = v
	}
	return out
}

// encryptClientRandom is the raw (unpadded) RSA of MS-RDPBCGR 5.3.4.1, little-endian, followed by
// 8 zero bytes.
func (k *serverKey) encryptClientRandom(random []byte) []byte {
	m := new(big.Int).SetBytes(reversed(random))
	c := new(big.Int).Exp(m, big.NewInt(int64(k.e)), k.n)
	return append(reversed(c.FillBytes(make([]byte, k.size))), make([]byte, 8)...)
}

// checkServerSecurity accepts only 128-bit RC4 with a key and a server random: anything else would
// mean no encryption (the logon in clear text) or 40/56-bit keys.
func checkServerSecurity(sd *pdu.ServerSecurityData) error {
	switch {
	case sd == nil || sd.EncryptionMethod == 0 || sd.EncryptionLevel == 0:
		return errors.New("the server offers standard RDP security without encryption; refusing to send the logon in clear text")
	case sd.EncryptionMethod != pdu.EncryptionFlag128Bit:
		return fmt.Errorf("the server chose RDP encryption method %#x; only 128-bit RC4 is supported (set the server's encryption level to High or Client Compatible)", sd.EncryptionMethod)
	case len(sd.ServerRandom) != 32:
		return errors.New("the server random is not 32 bytes")
	}
	return nil
}

// securityExchange sends the encrypted client random (Security Exchange PDU, MS-RDPBCGR 2.2.1.10)
// and switches the MCS layer and fast-path to encryption.
func (c *Client) securityExchange() error {
	key, err := serverPublicKey(c.serverSecurity.ServerCertificate)
	if err != nil {
		return err
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
	c.stdSec = newStdSecurity(c.mcsLayer, random, c.serverSecurity.ServerRandom)
	c.mcsLayer = c.stdSec
	c.fastPath.Decrypt = func(salted bool, data []byte) ([]byte, error) { return c.stdSec.open(data, salted) }
	return nil
}
