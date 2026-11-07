package tunnelconn

import (
	"fmt"
	"io"
	"net"
)

// SOCKS5 reply codes (RFC 1928).
const (
	socksGeneralFailure  = 1
	socksHostUnreachable = 4
	socksCmdUnsupported  = 7
	socksATypUnsupported = 8
)

func socks5Reply(code byte) []byte {
	return []byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0}
}

// socks5RestLen returns the bytes still needed after the 4-byte request header, or a SOCKS error code.
func socks5RestLen(header [4]byte, firstAddrByte byte) (int, byte) {
	if header[0] != 5 {
		return 0, socksGeneralFailure
	}
	if header[1] != 1 { // only CONNECT
		return 0, socksCmdUnsupported
	}
	switch header[3] { // address type
	case 1: // IPv4
		return 4 + 2, 0
	case 3: // domain name
		return 1 + int(firstAddrByte) + 2, 0
	case 4: // IPv6
		return 16 + 2, 0
	default:
		return 0, socksATypUnsupported
	}
}

// ParseSocks5Request parses a complete SOCKS5 CONNECT request into (host, port), or a SOCKS error code.
func ParseSocks5Request(req []byte) (string, uint16, byte) {
	if len(req) < 4 {
		return "", 0, socksGeneralFailure
	}
	header := [4]byte{req[0], req[1], req[2], req[3]}
	first := byte(0)
	if len(req) > 4 {
		first = req[4]
	}
	need, code := socks5RestLen(header, first)
	if code != 0 {
		return "", 0, code
	}
	rest := req[4:]
	if len(rest) != need {
		return "", 0, socksGeneralFailure
	}
	addr, portB := rest[:need-2], rest[need-2:]
	port := uint16(portB[0])<<8 | uint16(portB[1])
	var host string
	switch header[3] {
	case 1:
		host = net.IP(addr).String()
	case 4:
		host = net.IP(addr).String()
	default:
		host = string(addr[1:]) // skip the leading length byte
	}
	return host, port, 0
}

// socks5Handshake performs the no-auth greeting and reads the CONNECT request; on failure it replies
// with the error code before returning.
func socks5Handshake(s net.Conn) (string, uint16, error) {
	hello := make([]byte, 2)
	if _, err := io.ReadFull(s, hello); err != nil {
		return "", 0, err
	}
	methods := make([]byte, int(hello[1]))
	if _, err := io.ReadFull(s, methods); err != nil {
		return "", 0, err
	}
	if hello[0] != 5 || !contains(methods, 0) {
		_, _ = s.Write([]byte{5, 0xff})
		return "", 0, fmt.Errorf("SOCKS5: no acceptable auth method")
	}
	if _, err := s.Write([]byte{5, 0}); err != nil {
		return "", 0, err
	}
	req := make([]byte, 5)
	if _, err := io.ReadFull(s, req); err != nil {
		return "", 0, err
	}
	header := [4]byte{req[0], req[1], req[2], req[3]}
	need, code := socks5RestLen(header, req[4])
	if code != 0 {
		_, _ = s.Write(socks5Reply(code))
		return "", 0, fmt.Errorf("SOCKS5 request refused (%d)", code)
	}
	req = append(req, make([]byte, (4+need)-len(req))...)
	if _, err := io.ReadFull(s, req[5:]); err != nil {
		return "", 0, err
	}
	host, port, code := ParseSocks5Request(req)
	if code != 0 {
		_, _ = s.Write(socks5Reply(code))
		return "", 0, fmt.Errorf("SOCKS5 request refused (%d)", code)
	}
	return host, port, nil
}

func contains(b []byte, v byte) bool {
	for _, x := range b {
		if x == v {
			return true
		}
	}
	return false
}
