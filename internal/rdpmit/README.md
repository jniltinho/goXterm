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
- `rdp/connect.go`: a server that selects standard RDP security, or refuses TLS with
  SSL_NOT_ALLOWED_BY_SERVER, is refused (`ErrStandardSecurity`) before the logon info is sent, unless
  the session allows it (`SetAllowStandardSecurity`). Channel options are set per channel.
- `rdp/stdsec.go` (new): Standard RDP Security (MS-RDPBCGR 5.3), opt-in, ported from the Rust app
  (`archive/rust/crates/rdp/src/legacy.rs`): after SSL_NOT_ALLOWED_BY_SERVER the negotiation is retried
  once on a new TCP connection offering PROTOCOL_RDP; RC4 40/56/128 (FIPS and "no encryption" are
  refused); the server key comes from the proprietary certificate or the leaf of the X.509 chain, is
  size-checked and handed to `SetServerKeyCheck` as `RSA AA:BB:…` (the Rust trust format) before the
  Security Exchange; session keys, 4096-packet key update, MAC (plain and salted) on every slow-path
  PDU, the Client Info PDU and fast-path input/output (`protocol/fastpath`: `Protocol.Decrypt`,
  `InputEventPDU.SetFlags`); licensing PDUs go in clear until the server sets
  SEC_LICENSE_ENCRYPT_CS; redirection and clear PDUs above encryption level Low are refused.
  `protocol/pdu/basic_settings_exchange.go`: the Server Security Data is parsed inside its block
  with full reads and bounded lengths. `rdp/stdsec_test.go` (new): the Rust app's FreeRDP vectors,
  MS-RDPBCGR 4.8 RSA and 4.1.4 server data, a fake-server handshake and the negotiation retry.
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
- `rdp/client.go`, `rdp/connect.go`, `rdp/capabilities_exchange.go`: `SetKeyboardLayout` sends the
  session's keyboard layout (0 = server default) in the core data and input capabilities (upstream
  always sent US). The NLA-required negotiation error names the app's option instead of an
  environment variable.
- `rdp/get_update.go`, `rdp/capabilities_exchange.go`: a Deactivate All is answered with the
  deactivation-reactivation sequence (capabilities exchange, finalization, refresh) instead of closing
  the session — Windows Server 2008 R2 deactivates right after every logon. The capabilities
  exchange waits for the Demand Active and skips other PDUs (upstream decoded the first one blindly).
- `rdp/tls.go`: a TLS 1.0-only server reports which session option to tick.
- `protocol/pdu/secure_settings_exchange.go`: performance flags as the Rust app sent them (no full
  window drag, no menu animations, font smoothing); upstream sent 0.
- `codec/planar.go`: scanline deltas wrap modulo 256 (upstream saturated them, streaking columns) and
  the alpha plane is ignored (Windows sends 0); `codec/planar_test.go` (new).
- `codec/nscodec.go`: rewritten from MS-RDPNSC — upstream's RLE was not the NSCodec scheme and Co/Cg
  were unsigned, so no Windows tile decoded; rows are bottom-up in surface bits.
  `codec/nscodec_test.go` (new).
- `protocol/pdu/cap_surface.go`: the Bitmap Codecs capability offers NSCodec only (id 1); upstream
  also offered RemoteFX-Image without properties. `rdp/capabilities_exchange.go` offers surface
  commands and codecs only to RDP 8+ servers (server core version >= 0x00080005); xrdp and 2008 R2
  report 0x00080004.
- `protocol/pdu/frame_ack.go`: PDUTYPE2_FRAME_ACKNOWLEDGE is 0x38 (upstream wrote decimal 38, and the
  server dropped the connection at the first acknowledged frame); `rdp/frame_ack.go` restored.
- `protocol/pdu/capabilities_exchange.go`: each capability set is read as exactly its declared length
  and parsed inside it (upstream parsed off the stream, so a set laid out differently — Windows
  Server 2008 at 32 bpp — consumed the next ones and the Demand Active failed with EOF); a set that
  does not parse is kept by type; a missing sessionId is tolerated.
  `protocol/pdu/demand_active_test.go` (new).
- `rdp/static_channels.go`, `rdp/capabilities_exchange.go`, `rdp/connection_finalization.go`:
  static-channel PDUs (e.g. cliprdr Monitor Ready) are dispatched in every receive loop, also while
  waiting for the Demand Active and during finalization (they were dropped there).
- `rdp/send_input_event.go`, `rdp/get_update.go`: input is not sent between a Deactivate All and the
  end of reactivation (MS-RDPBCGR 1.3.1.3).
- `rdp/nla.go`: bodgit/ntlmssp indexes AV-pair values from the server challenge without length
  checks (a 1-byte MsvAvFlags panics); the call is guarded and becomes an error. `rdp/nla_test.go`.
- `rdp/security_test.go` (new): tests for the changes above.
