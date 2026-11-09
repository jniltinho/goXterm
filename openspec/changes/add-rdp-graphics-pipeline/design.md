# Design

## Context

`internal/rdpmit` is go-rdp (MIT) vendored and patched; `internal/rdpconn` is its only user
(`internal/archtest`). Updates reach the GUI as PNG rectangles via `onFrame`. Static channels are
registered with `AddStaticChannel` and get header-included chunks from every receive loop
(`dispatchChannel`). Standard RDP Security wraps `mcsLayer`, so channel traffic is already encrypted.

## Decisions

### D1 Transport
- `drdynvc` is joined as a static channel (options INITIALIZED | ENCRYPT_RDP). Static chunks
  (CHANNEL_FLAG_FIRST/LAST) are reassembled, bounded to 1 MiB (a DVC PDU fits one chunk; the bound only stops abuse).
- Channel handlers return an error; a non-nil error ends `GetUpdate` (and the capabilities/finalization
  loops) so a GFX failure becomes the session error.
- DVC (`protocol/drdynvc`, written here; upstream go-rdp's package was not vendored because its ZGFX
  is not the MS-RDPEGFX algorithm): caps answered with version 2 (never 3: no compressed DVC),
  variable-width channel ids, CREATE answered (OK for the graphics channel, NO_LISTENER otherwise),
  DATA_FIRST/DATA reassembly bounded to 32 MiB per message, CLOSE drops state and is answered.
- `GetUpdate` becomes a loop: channel-only PDUs no longer recurse, so a long GFX-only stream cannot grow
  the stack.

### D2 RDPGFX (`internal/rdpmit/rdpgfx`)
- ZGFX: E0 single / E1 multipart, 2,500,000-byte history shared across PDUs (raw segments update it
  too), at most 65,535 bytes per segment and 32 MiB per message (checked before allocating). One
  decoded payload may hold several RDPGFX PDUs. Client PDUs are sent plain (no ZGFX).
- Caps: CapsAdvertise with RDPGFX_CAPVERSION_81 and RDPGFX_CAPVERSION_8, both THINCLIENT, no
  AVC420_ENABLED. CapsConfirm must be one of them; anything else ends the session. THINCLIENT means
  the server uses a 16 MiB / 4,096-slot (one-based) surface cache and never progressive RemoteFX;
  WireToSurface2 is therefore a protocol error.
- State: surfaces (≤ 8192×8192 each, ≤ 64 Mpx in all, ≤ 1024 surfaces), output mapping, cache slots
  (≤ 4,096, ≤ 16 MiB in all, the server's own accounting), frame begin/end.
- Frame ack after EndFrame, once the frame's output rectangles have been produced: frameId of that
  frame, connection-wide totalFramesDecoded, queueDepth 0 (unavailable). Never 0xFFFFFFFF (it suspends
  acks). No CacheImportOffer (no persistent cache).
- Pixels are kept as 32-bit BGRX/BGRA (little-endian uint32 0xAARRGGBB) top-down per surface.

### D3 Codecs
- Uncompressed: XRGB/ARGB 8888, top-down.
- Planar: `codec/planar.go` gets a GFX entry point: top-down output, alpha plane kept, color loss level
  and chroma subsampling (YCoCg) handled; legacy bitmap callers unchanged.
- ClearCodec (`codec/clear`, written from MS-RDPEGFX 2.2.4.1 and checked against FreeRDP): residual
  layer, bands with vBar (32,768) / shortVBar (16,384) caches, subcodecs (uncompressed, NSCodec,
  RLEX), glyph cache (4,000 entries, ≤ 1,024 pixels each). Caches persist for the connection.
- RemoteFX: go-rdp `codec/rfx` vendored, with fixes: one entropy mode (from the context block) for all
  three components, quant indices range-checked (error), tile count/position bounded, region rects
  clip the tiles.
- Alpha: CODEC_ALPHA (raw and RLE) replaces the alpha channel only.

### D4 Composition (`internal/rdpconn`)
- The output is the mapped surface(s) blitted into a session framebuffer; changed rectangles per frame
  are merged and PNG-encoded as today; GFX frame ids never go through `AckFrames` (that is the legacy
  surface-bits acknowledge).
- The GFX flag (RNS_UD_CS_SUPPORT_DYNVC_GFX_PROTOCOL) is set only on connections from `rdpconn`, and
  only once all mandatory codecs are in place (milestone 5); `RXT_RDP_NO_GFX=1` keeps it off.

## Risks
- A server that confirms GFX but uses a codec this client does not decode: the session ends with an
  error naming `RXT_RDP_NO_GFX=1` (the bitmap path cannot be brought back without a reconnect).
- ClearCodec has few public vectors; Windows traffic is the real check (milestone 5).
