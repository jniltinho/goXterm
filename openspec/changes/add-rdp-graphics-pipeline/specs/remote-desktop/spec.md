# Spec Delta

## ADDED Requirements

### Requirement: RDP graphics pipeline
RDP sessions SHALL use the RDP Graphics Pipeline (MS-RDPEGFX version 8/8.1, without H.264 and without
progressive RemoteFX) when the server opens it, decoding uncompressed, planar, ClearCodec, RemoteFX and
alpha content losslessly where the server sends it losslessly. Servers that do not open it SHALL keep
the bitmap and surface-bits path. Setting `RXT_RDP_NO_GFX=1` SHALL disable the pipeline.

#### Scenario: Windows RDP 8+ server
- **WHEN** the user connects to Windows Server 2012 or later (or Windows 10/11)
- **THEN** the desktop is drawn from the graphics pipeline and a gradient wallpaper shows no banding

#### Scenario: Server without the graphics pipeline
- **WHEN** the server never opens the graphics channel (Windows Server 2008 R2, xrdp without GFX)
- **THEN** the desktop is drawn from bitmap updates as before

#### Scenario: Unsupported or malformed graphics data
- **WHEN** the server sends a codec this client does not decode, a malformed graphics PDU, or exceeds a resource limit
- **THEN** the session ends with an error that names `RXT_RDP_NO_GFX=1`; the client never crashes and never shows a blank desktop as if connected
