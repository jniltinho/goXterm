# rdpmit — vendored go-rdp

RDP client code vendored from [rcarmo/go-rdp](https://github.com/rcarmo/go-rdp) (MIT, `LICENSE`
here), branch `master`, commit `5b081feb69ef89d1baae663a28f0d8d8156c4156`. Upstream keeps its client
under `internal/`, so the needed packages are copied here; `rustxterm-go/internal/rdpconn` is their
only user (enforced by `internal/archtest`).

## What was copied

Non-test `.go` files of upstream `internal/rdp`, `internal/auth`, `internal/codec`,
`internal/logging` and `internal/protocol/{encoding,gcc,mcs,pdu,tpkt,x224,fastpath}`, same layout
(`internal/X` → `internal/rdpmit/X`). Upstream tests were not copied (they need testify).

## What was changed

- Import paths `github.com/rcarmo/go-rdp/internal/...` → `rustxterm-go/internal/rdpmit/...`;
  `gofmt` applied.
- Removed (not used here): `rdp/audio.go`, `rdp/display_control.go`, `rdp/multitransport.go`
  (UDP, pulled in pion/dtls), `rdp/rail.go`, `rdp/remote_app.go`, `rdp/frame_ack.go`,
  `auth/server.go`, `codec/nscodec_encode.go`, `fastpath/{rdpgfx_codecs,surface_commands,update_events}.go`,
  the `internal/config` package; the matching fields and branches in `rdp/client.go`,
  `rdp/connect.go`, `rdp/get_update.go`, `rdp/close.go`, `rdp/connection_finalization.go` and
  `rdp/capabilities_exchange.go`.
- `rdp/tls.go`, `rdp/nla.go`: TLS settings no longer come from the global config / environment;
  `SetTLSClientConfig` supplies them (certificate trust is decided by the caller's
  `VerifyConnection` during the handshake). NLA keeps upstream's TLS 1.2 cap when no MaxVersion is set.
- `rdp/connect.go`: a server that selects standard RDP security is refused (`ErrStandardSecurity`)
  before the logon info is sent — this client does not implement standard RDP security (RC4), so
  continuing would send the credentials unencrypted. Channel options are set per channel.
- `rdp/licensing.go` (new) and `rdp/connect.go`: a server License Request (xrdp always sends one)
  is answered with a Client New License Request; `pdu/licensing.go` parses a LICENSE_ERROR_MESSAGE
  only for ERROR_ALERT. Full MS-RDPELE (encrypted pre-master secret, platform challenge) is not
  implemented.
- `protocol/fastpath/receive.go`: the fast-path length counts the whole PDU, header included
  (upstream read 2–3 bytes too many and lost sync on the next PDU).
- `protocol/pdu/input_events.go`: fast-path input event header bit order fixed (eventCode in the top
  3 bits); upstream's order turned a key release into a second key press.
- `rdp/static_channels.go` (new): `SetTLSClientConfig`, `AddStaticChannel`, `SendStaticChannel`;
  `rdp/get_update.go` hands chunks of registered static channels (e.g. `cliprdr`) to their handler.
- `auth/ntlm.go` and `auth/md4.go` removed: their NTLM code follows the GPL-3.0 grdp NTLM too
  closely (same API and structure), so `rdp/nla.go` uses
  [github.com/bodgit/ntlmssp](https://github.com/bodgit/ntlmssp) (BSD-3-Clause, a Go module
  dependency) for NTLMv2, MIC and sealing. The server's `pubKeyAuth` is now mandatory (upstream sent
  the credentials when it was missing) and a CredSSP `errorCode` is reported as a readable error
  (wrong password, expired password, locked or disabled account).
- `rdp/security_test.go` (new): tests for the changes above.
