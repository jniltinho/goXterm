package rdp

import "errors"

// ErrUnsupportedRequestedProtocol indicates that the server selected a protocol
// that this client does not support.
var (
	ErrUnsupportedRequestedProtocol = errors.New("unsupported requested protocol")

	// ErrStandardSecurity (rustxterm): the server chose standard RDP security, which this client
	// does not implement; it is refused before any credential is sent.
	ErrStandardSecurity = errors.New("the server only offers standard RDP security (no TLS), which is not supported")
)
