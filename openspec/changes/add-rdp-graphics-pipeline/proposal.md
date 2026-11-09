# Proposal

## Why

On RDP 8+ servers (Windows Server 2012 and later, Windows 10/11) the Go client receives the desktop as
NSCodec surface bits, which Windows encodes lossily: wallpaper gradients are banded and colored edges are
soft, visibly worse than MobaXterm/FreeRDP. Those clients use the RDP Graphics Pipeline (MS-RDPEGFX),
where Windows sends lossless ClearCodec/planar for UI content and RemoteFX for photographic regions.
Windows Server 2008 R2 (RDP 7) already sends lossless planar bitmaps and is not affected.

## What Changes

- Dynamic virtual channels (MS-RDPEDYC) over the `drdynvc` static channel: capabilities (version 1/2),
  create/close, DATA_FIRST/DATA reassembly; only `Microsoft::Windows::RDS::Graphics` is accepted.
- RDPGFX client, version 8 and 8.1, THINCLIENT flag, no AVC/H.264, no progressive RemoteFX: ZGFX
  (RDP8 bulk) decompression, surfaces, solid fill, surface-to-surface, surface cache, frame
  acknowledgements, and the codecs uncompressed, planar, ClearCodec (with its NSCodec/RLEX subcodecs and
  glyph/vBar caches), RemoteFX (CAVIDEO) and alpha.
- Composition of mapped surfaces into the session framebuffer; the frontend keeps receiving PNG
  rectangles through the existing frame callback.
- Servers that do not open the graphics channel (2008 R2, xrdp without GFX) keep today's bitmap and
  surface-bits path. `RXT_RDP_NO_GFX=1` disables the pipeline for troubleshooting. No stored format
  changes.
- A GFX failure (unsupported codec, malformed PDU, resource limit) ends the session with a clear error
  naming `RXT_RDP_NO_GFX=1`, never a blank screen: once the graphics channel is open the server sends
  no bitmap updates.

## Impact

- `internal/rdpmit/rdp` (static channel errors, iterative update loop, GFX flag in the core data),
  new `internal/rdpmit/protocol/drdynvc`, `internal/rdpmit/rdpgfx`, `internal/rdpmit/codec/clear`,
  vendored `internal/rdpmit/codec/rfx` (go-rdp, MIT), `internal/rdpmit/codec/planar.go`,
  `internal/rdpconn` (channel wiring, composition).
- Spec: `remote-desktop` (graphics pipeline requirement).
