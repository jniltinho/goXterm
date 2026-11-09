# Tasks

## 1. Transport, capabilities and ZGFX (milestone 1)

- [x] 1.1 Static channel handlers return errors; `dispatchChannel` propagates them from every receive loop; `GetUpdate` is iterative (no recursion on channel-only or non-bitmap PDUs)
- [x] 1.2 `SetEnableGFX` sets RNS_UD_CS_SUPPORT_DYNVC_GFX_PROTOCOL in the client core data (off by default)
- [x] 1.3 `protocol/drdynvc`: static chunk reassembly (bounded), caps v2 reply, create (graphics accepted, others refused), DATA_FIRST/DATA reassembly (bounded), close; malformed input returns errors
- [x] 1.4 `rdpgfx` ZGFX decompressor (E0/E1, 2.5 MB history, 65,535 per segment, aggregate limits); MS-RDPEGFX fox vectors and malformed-input tests
- [x] 1.5 RDPGFX PDU header parsing (several PDUs per payload), CapsAdvertise (8.1 + 8, THINCLIENT), CapsConfirm check; fake-server test over drdynvc

## 2. Surfaces, cache, uncompressed and planar, frame ack (milestone 2)

- [x] 2.1 ResetGraphics, Create/DeleteSurface, MapSurfaceToOutput, SolidFill, SurfaceToSurface, SurfaceToCache, CacheToSurface, EvictCacheEntry, DeleteEncodingContext, with resource limits and range checks
- [x] 2.2 Start/EndFrame and FrameAcknowledge (frame id, totalFramesDecoded, queueDepth 0)
- [x] 2.3 WireToSurface1 uncompressed and planar (top-down, alpha, color loss, subsampling); WireToSurface2 and unknown codecs end the session with an error naming RXT_RDP_NO_GFX
- [x] 2.4 `rdpconn` composes mapped surfaces into the framebuffer and emits PNG rectangles per frame; GFX errors end the session
- [x] 2.5 Fake-server sequence test: caps → reset → create surface → map → wire-to-surface → end frame → ack

## 3. ClearCodec (milestone 3)

- [x] 3.1 `codec/clear`: residual, bands/vBars with vBar and shortVBar caches, subcodecs (uncompressed, NSCodec, RLEX), glyph cache
- [x] 3.2 MS-RDPEGFX 4.1.1.1 examples, handcrafted vectors with expected pixels, malformed-input tests

## 4. RemoteFX and alpha (milestone 4)

- [ ] 4.1 Vendor go-rdp `codec/rfx` with its tests; fix per-context entropy mode, quant index checks, tile bounds, region clipping
- [ ] 4.2 CAVIDEO in WireToSurface1; CODEC_ALPHA (raw and RLE)

## 5. Live verification and enabling (milestone 5)

- [ ] 5.1 `rdpconn` sets the GFX flag unless `RXT_RDP_NO_GFX=1`; one info log when the graphics channel opens
- [ ] 5.2 Windows RDP 8+ VM: desktop over GFX (ClearCodec, planar, RemoteFX seen), gradient wallpaper not banded, input works, long session, reconnect
- [ ] 5.3 Lab xrdp still connects (`make lab-test`); `make check test layers guard` pass
