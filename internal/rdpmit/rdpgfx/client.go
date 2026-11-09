package rdpgfx

import (
	"encoding/binary"
	"image"
	"sort"
)

// Resource limits: every size and index from the server is untrusted.
const (
	maxSide          = 8192     // surface and output side
	maxSurfacePixels = 64 << 20 // all surfaces together
	maxSurfaces      = 1024     //
	thinCacheSlots   = 4096     // RDPGFX_CAPS_FLAG_THINCLIENT: 4,096 one-based slots
	fullCacheSlots   = 25600    // without THINCLIENT (never advertised, but bounded anyway)
	maxCacheBytes    = 64 << 20 // the server accounts 16 MiB (thin) / 100 MiB; our own bound
	maxMergedRects   = 64       // dirty rectangles handed over per frame before merging
	pixelXRGB        = 0x20     // GFX_PIXEL_FORMAT_XRGB_8888
	pixelARGB        = 0x21     // GFX_PIXEL_FORMAT_ARGB_8888
)

// RDPGFX_CODECID_* (MS-RDPEGFX 2.2.1.4).
const (
	codecUncompressed = 0x0000
	codecCAVideo      = 0x0003
	codecClear        = 0x0008
	codecProgressive  = 0x0009
	codecPlanar       = 0x000A
	codecAVC420       = 0x000B
	codecAlpha        = 0x000C
	codecAVC444       = 0x000E
	codecAVC444v2     = 0x000F
)

// Frame is one finished graphics frame: the session framebuffer (opaque RGBA, the whole desktop) and
// the rectangles of it that changed. The callee must not keep FB after returning.
type Frame struct {
	FB    *image.RGBA
	Dirty []image.Rectangle
}

// Client is the graphics pipeline channel. OnFrame gets the output after every frame (and after
// updates outside a frame); it runs on the session's receive loop, before the frame is acknowledged.
// OnOpen, when set, runs once the server confirmed the capabilities.
type Client struct {
	OnFrame func(Frame)
	OnOpen  func(version, flags uint32)

	write     func([]byte) error
	zgfx      ZGFX
	confirmed bool
	flags     uint32
	slots     int

	fb       *image.RGBA // output (session desktop)
	surfaces map[uint16]*surface
	pixels   int // allocated surface pixels
	cache    map[uint16]*cacheEntry
	cacheB   int

	inFrame      bool
	frameID      uint32
	totalDecoded uint32
	dirty        []rect // output rectangles changed since the last OnFrame

	codecs codecState
}

// surface is an off-screen RGBA surface, padded to a multiple of 16 like FreeRDP's.
type surface struct {
	w, h    int // created size (mapped area)
	pw, ph  int // padded size
	pix     []byte
	alpha   bool // ARGB
	mapped  bool
	ox, oy  int // output origin
	invalid []rect
}

type cacheEntry struct {
	w, h int
	pix  []byte
}

// Open sends the capabilities (MS-RDPEGFX 3.2.5.1): the channel is unusable until confirmed.
func (c *Client) Open(write func([]byte) error) error {
	c.write = write
	return write(capsAdvertise())
}

// Close drops the channel state; a new channel starts over.
func (c *Client) Close() {
	*c = Client{OnFrame: c.OnFrame, OnOpen: c.OnOpen}
}

// Data handles one channel message: ZGFX-wrapped, possibly several RDPGFX PDUs.
func (c *Client) Data(msg []byte) error {
	p, err := c.zgfx.Decompress(msg)
	if err != nil {
		return fail("%v", err)
	}
	for len(p) > 0 {
		if len(p) < headerSize {
			return fail("truncated PDU header")
		}
		cmd := binary.LittleEndian.Uint16(p)
		n := binary.LittleEndian.Uint32(p[4:])
		if n < headerSize || uint64(n) > uint64(len(p)) {
			return fail("PDU 0x%04X of %d bytes in %d", cmd, n, len(p))
		}
		if err := c.pdu(cmd, p[headerSize:n]); err != nil {
			return err
		}
		p = p[n:]
	}
	if !c.inFrame {
		return c.flush(false)
	}
	return nil
}

func (c *Client) pdu(cmd uint16, body []byte) error {
	r := &reader{p: body}
	if !c.confirmed && cmd != cmdCapsConfirm {
		return fail("PDU 0x%04X before the capabilities were confirmed", cmd)
	}
	var err error
	switch cmd {
	case cmdCapsConfirm:
		err = c.capsConfirm(r)
	case cmdResetGraphics:
		err = c.resetGraphics(r)
	case cmdCreateSurface:
		err = c.createSurface(r)
	case cmdDeleteSurface:
		id := r.u16()
		if s := c.surfaces[id]; s != nil && !r.bad {
			c.pixels -= s.pw * s.ph
			delete(c.surfaces, id)
		}
	case cmdMapSurfaceToOutput:
		err = c.mapSurface(r)
	case cmdStartFrame:
		r.u32() // timestamp
		c.frameID, c.inFrame = r.u32(), true
	case cmdEndFrame:
		id := r.u32()
		if r.bad {
			break
		}
		c.totalDecoded++
		if err = c.flush(true); err == nil {
			c.inFrame = false
			err = c.write(frameAck(id, c.totalDecoded))
		}
	case cmdSolidFill:
		err = c.solidFill(r)
	case cmdSurfaceToSurface:
		err = c.surfaceToSurface(r)
	case cmdSurfaceToCache:
		err = c.surfaceToCache(r)
	case cmdCacheToSurface:
		err = c.cacheToSurface(r)
	case cmdEvictCacheEntry:
		slot := r.u16()
		if e := c.cache[slot]; e != nil && !r.bad {
			c.cacheB -= len(e.pix)
			delete(c.cache, slot)
		}
	case cmdWireToSurface1:
		err = c.wireToSurface1(r)
	case cmdWireToSurface2:
		// progressive RemoteFX: excluded by THINCLIENT, so the server broke the confirmed caps
		return fail("the server sent progressive RemoteFX (WireToSurface2), which was not advertised")
	case cmdDeleteEncodingCtx:
		r.u16() // surfaceId
		r.u32() // codecContextId (WireToSurface2 only, never accepted)
	default:
		return fail("unsupported PDU 0x%04X", cmd)
	}
	if err != nil {
		return err
	}
	if r.bad {
		return fail("truncated PDU 0x%04X", cmd)
	}
	return nil
}

func (c *Client) capsConfirm(r *reader) error {
	version, n, flags := r.u32(), r.u32(), r.u32()
	if r.bad || n < 4 {
		return fail("malformed capabilities confirm")
	}
	if version != CapVersion8 && version != CapVersion81 {
		return fail("the server confirmed capability version 0x%08X, not 8 or 8.1", version)
	}
	if flags&capsAVC420 != 0 {
		return fail("the server confirmed AVC420 (H.264), which was not advertised")
	}
	c.confirmed, c.flags, c.slots = true, flags, fullCacheSlots
	if flags&capsThin != 0 {
		c.slots = thinCacheSlots
	}
	c.totalDecoded = 0
	if c.OnOpen != nil {
		c.OnOpen(version, flags)
	}
	return nil
}

func (c *Client) resetGraphics(r *reader) error {
	w, h, monitors := int(r.u32()), int(r.u32()), r.u32()
	if r.bad || w <= 0 || h <= 0 || w > maxSide || h > maxSide || monitors > 16 {
		return fail("reset graphics to %d×%d with %d monitors", w, h, monitors)
	}
	// the monitor definitions and the padding to 340 bytes are not needed: one output
	r.p = nil
	c.fb = image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 3; i < len(c.fb.Pix); i += 4 {
		c.fb.Pix[i] = 0xFF
	}
	for _, s := range c.surfaces { // FreeRDP: surfaces are kept and cleared to white
		for i := range s.pix {
			s.pix[i] = 0xFF
		}
		s.invalid = nil
	}
	c.dirty = append(c.dirty[:0], rect{0, 0, w, h})
	return nil
}

func align16(n int) int { return (n + 15) &^ 15 }

func (c *Client) createSurface(r *reader) error {
	id, w, h, format := r.u16(), int(r.u16()), int(r.u16()), r.u8()
	if r.bad {
		return nil
	}
	if format != pixelXRGB && format != pixelARGB {
		return fail("surface pixel format 0x%02X", format)
	}
	pw, ph := align16(w), align16(h)
	if w <= 0 || h <= 0 || pw > maxSide || ph > maxSide {
		return fail("surface of %d×%d", w, h)
	}
	if old := c.surfaces[id]; old != nil {
		c.pixels -= old.pw * old.ph
		delete(c.surfaces, id)
	}
	if len(c.surfaces) >= maxSurfaces || c.pixels+pw*ph > maxSurfacePixels {
		return fail("too many surfaces (%d, %d pixels)", len(c.surfaces), c.pixels+pw*ph)
	}
	s := &surface{w: w, h: h, pw: pw, ph: ph, pix: make([]byte, pw*ph*4), alpha: format == pixelARGB}
	for i := range s.pix {
		s.pix[i] = 0xFF
	}
	if c.surfaces == nil {
		c.surfaces = map[uint16]*surface{}
	}
	c.surfaces[id] = s
	c.pixels += pw * ph
	return nil
}

func (c *Client) surface(id uint16) (*surface, error) {
	if s := c.surfaces[id]; s != nil {
		return s, nil
	}
	return nil, fail("unknown surface %d", id)
}

func (c *Client) mapSurface(r *reader) error {
	id := r.u16()
	r.u16() // reserved
	x, y := r.u32(), r.u32()
	if r.bad {
		return nil
	}
	s, err := c.surface(id)
	if err != nil {
		return err
	}
	if x > maxSide || y > maxSide {
		return fail("surface %d mapped at %d,%d", id, x, y)
	}
	s.mapped, s.ox, s.oy, s.invalid = true, int(x), int(y), nil
	return nil
}

// invalidate records a changed surface area (surface coordinates).
func (s *surface) invalidate(a rect) {
	if a.empty() {
		return
	}
	for i, b := range s.invalid { // cheap merge of repeated or contained updates
		if b.union(a) == b {
			return
		}
		if a.union(b) == a {
			s.invalid[i] = a
			return
		}
	}
	if len(s.invalid) >= maxMergedRects {
		u := a
		for _, b := range s.invalid {
			u = u.union(b)
		}
		s.invalid = append(s.invalid[:0], u)
		return
	}
	s.invalid = append(s.invalid, a)
}

// flush copies the invalid areas of mapped surfaces to the output and hands the changed output
// over. Output pixels are opaque: the desktop has no alpha (FreeRDP draws BGRX).
func (c *Client) flush(endFrame bool) error {
	if c.fb == nil {
		return nil
	}
	ids := make([]int, 0, len(c.surfaces))
	for id := range c.surfaces {
		ids = append(ids, int(id))
	}
	sort.Ints(ids) // deterministic order where surfaces overlap
	ob := c.fb.Bounds()
	for _, id := range ids {
		s := c.surfaces[uint16(id)]
		if !s.mapped {
			s.invalid = nil
			continue
		}
		for _, a := range s.invalid {
			a = a.intersect(rect{0, 0, s.w, s.h})
			d := rect{a.x0 + s.ox, a.y0 + s.oy, a.x1 + s.ox, a.y1 + s.oy}.intersect(rect{0, 0, ob.Dx(), ob.Dy()})
			if d.empty() {
				continue
			}
			for y := d.y0; y < d.y1; y++ {
				src := s.pix[((y-s.oy)*s.pw+d.x0-s.ox)*4:]
				dst := c.fb.Pix[y*c.fb.Stride+d.x0*4:]
				for i := 0; i < d.w()*4; i += 4 {
					dst[i], dst[i+1], dst[i+2], dst[i+3] = src[i], src[i+1], src[i+2], 0xFF
				}
			}
			c.dirty = append(c.dirty, d)
		}
		s.invalid = s.invalid[:0]
	}
	if len(c.dirty) == 0 || c.OnFrame == nil {
		c.dirty = c.dirty[:0]
		return nil
	}
	out := mergeRects(c.dirty)
	c.dirty = c.dirty[:0]
	c.OnFrame(Frame{FB: c.fb, Dirty: out})
	return nil
}

// mergeRects returns the rectangles, or their bounding box when there are many of them or they
// cover most of it anyway. shortcut: no region algebra, a bounding box can repaint unchanged
// pixels; upgrade if PNG encoding of large boxes shows in profiles.
func mergeRects(rs []rect) []image.Rectangle {
	var u rect
	area := 0
	for _, r := range rs {
		u = u.union(r)
		area += r.w() * r.h()
	}
	if len(rs) > maxMergedRects || area >= u.w()*u.h()/2 {
		return []image.Rectangle{image.Rect(u.x0, u.y0, u.x1, u.y1)}
	}
	out := make([]image.Rectangle, len(rs))
	for i, r := range rs {
		out[i] = image.Rect(r.x0, r.y0, r.x1, r.y1)
	}
	return out
}

func (c *Client) solidFill(r *reader) error {
	id := r.u16()
	px := r.take(4) // B, G, R, XA (alpha ignored, as FreeRDP)
	n := int(r.u16())
	if r.bad {
		return nil
	}
	s, err := c.surface(id)
	if err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		a := r.rect16()
		if r.bad {
			return nil
		}
		if a.x0 > a.x1 || a.y0 > a.y1 || a.x0 > s.pw || a.y0 > s.ph {
			return fail("solid fill rectangle outside surface %d", id)
		}
		a = a.intersect(rect{0, 0, s.pw, s.ph})
		for y := a.y0; y < a.y1; y++ {
			row := s.pix[(y*s.pw+a.x0)*4 : (y*s.pw+a.x1)*4]
			for x := 0; x < len(row); x += 4 {
				row[x], row[x+1], row[x+2], row[x+3] = px[2], px[1], px[0], 0xFF
			}
		}
		s.invalidate(a)
	}
	return nil
}

// copyRect copies a w×h block between RGBA buffers; overlapping copies within one buffer are safe.
func copyRect(dst []byte, dstW, dx, dy int, src []byte, srcW, sx, sy, w, h int) {
	if dy > sy && &dst[0] == &src[0] { // moving down in place: copy rows bottom-up
		for y := h - 1; y >= 0; y-- {
			copy(dst[((dy+y)*dstW+dx)*4:((dy+y)*dstW+dx+w)*4], src[((sy+y)*srcW+sx)*4:])
		}
		return
	}
	for y := 0; y < h; y++ {
		copy(dst[((dy+y)*dstW+dx)*4:((dy+y)*dstW+dx+w)*4], src[((sy+y)*srcW+sx)*4:])
	}
}

func (c *Client) surfaceToSurface(r *reader) error {
	srcID, dstID, src, n := r.u16(), r.u16(), r.rect16(), int(r.u16())
	if r.bad {
		return nil
	}
	ss, err := c.surface(srcID)
	if err != nil {
		return err
	}
	ds, err := c.surface(dstID)
	if err != nil {
		return err
	}
	if !src.within(ss.pw, ss.ph) {
		return fail("surface-to-surface source outside surface %d", srcID)
	}
	for i := 0; i < n; i++ {
		x, y := int(r.u16()), int(r.u16())
		if r.bad {
			return nil
		}
		d := rect{x, y, x + src.w(), y + src.h()}
		if !d.within(ds.pw, ds.ph) {
			return fail("surface-to-surface destination outside surface %d", dstID)
		}
		if !d.empty() {
			copyRect(ds.pix, ds.pw, x, y, ss.pix, ss.pw, src.x0, src.y0, src.w(), src.h())
		}
		ds.invalidate(d)
	}
	return nil
}

func (c *Client) surfaceToCache(r *reader) error {
	id := r.u16()
	r.take(8) // cacheKey (persistent cache only)
	slot, src := r.u16(), r.rect16()
	if r.bad {
		return nil
	}
	s, err := c.surface(id)
	if err != nil {
		return err
	}
	if slot == 0 || int(slot) > c.slots {
		return fail("cache slot %d", slot)
	}
	if !src.within(s.pw, s.ph) || src.empty() {
		return fail("surface-to-cache rectangle outside surface %d", id)
	}
	if old := c.cache[slot]; old != nil {
		c.cacheB -= len(old.pix)
	}
	n := src.w() * src.h() * 4
	if c.cacheB+n > maxCacheBytes {
		return fail("surface cache over %d bytes", maxCacheBytes)
	}
	e := &cacheEntry{w: src.w(), h: src.h(), pix: make([]byte, n)}
	copyRect(e.pix, e.w, 0, 0, s.pix, s.pw, src.x0, src.y0, e.w, e.h)
	if c.cache == nil {
		c.cache = map[uint16]*cacheEntry{}
	}
	c.cache[slot] = e
	c.cacheB += n
	return nil
}

func (c *Client) cacheToSurface(r *reader) error {
	slot, id, n := r.u16(), r.u16(), int(r.u16())
	if r.bad {
		return nil
	}
	s, err := c.surface(id)
	if err != nil {
		return err
	}
	e := c.cache[slot]
	if e == nil {
		return fail("empty cache slot %d", slot)
	}
	for i := 0; i < n; i++ {
		x, y := int(r.u16()), int(r.u16())
		if r.bad {
			return nil
		}
		d := rect{x, y, x + e.w, y + e.h}
		if !d.within(s.pw, s.ph) {
			return fail("cache-to-surface destination outside surface %d", id)
		}
		copyRect(s.pix, s.pw, x, y, e.pix, e.w, 0, 0, e.w, e.h)
		s.invalidate(d)
	}
	return nil
}

func (c *Client) wireToSurface1(r *reader) error {
	id, codec, format := r.u16(), r.u16(), r.u8()
	dst := r.rect16()
	n := r.u32()
	data := r.take(int(min(n, uint32(len(r.p)+1)))) // a length past the PDU fails the read
	if r.bad {
		return nil
	}
	s, err := c.surface(id)
	if err != nil {
		return err
	}
	if !dst.within(s.pw, s.ph) || dst.empty() {
		return fail("codec 0x%04X rectangle %v outside surface %d (%d×%d)", codec, dst, id, s.pw, s.ph)
	}
	if format != pixelXRGB && format != pixelARGB {
		return fail("pixel format 0x%02X", format)
	}
	var changed []rect
	switch codec {
	case codecUncompressed:
		err = uncompressed(s, dst, data)
	case codecPlanar:
		err = c.planar(s, dst, data)
	case codecClear:
		changed, err = c.clear(s, dst, data)
	case codecCAVideo:
		changed, err = c.remoteFX(s, dst, data)
	case codecAlpha:
		err = alphaCodec(s, dst, data)
	case codecAVC420, codecAVC444, codecAVC444v2:
		return fail("the server sent H.264 (codec 0x%04X), which was not advertised", codec)
	case codecProgressive:
		return fail("the server sent progressive RemoteFX, which was not advertised")
	default:
		return fail("unsupported codec 0x%04X", codec)
	}
	if err != nil {
		return fail("codec 0x%04X: %v", codec, err)
	}
	if changed == nil {
		s.invalidate(dst)
	}
	for _, a := range changed {
		s.invalidate(a)
	}
	return nil
}

// blit stores top-down RGBA (w×h) at the destination rectangle; alpha is kept only on ARGB
// surfaces.
func (s *surface) blit(dst rect, rgba []byte) {
	w := dst.w()
	for y := 0; y < dst.h(); y++ {
		row := s.pix[((dst.y0+y)*s.pw+dst.x0)*4:]
		src := rgba[y*w*4 : (y+1)*w*4]
		copy(row, src)
		if !s.alpha {
			for i := 3; i < len(src); i += 4 {
				row[i] = 0xFF
			}
		}
	}
}

// uncompressed is RDPGFX_CODECID_UNCOMPRESSED: top-down 32 bpp B, G, R, X/A rows.
func uncompressed(s *surface, dst rect, data []byte) error {
	w, h := dst.w(), dst.h()
	if len(data) < w*h*4 {
		return errShortData
	}
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h*4; i += 4 {
		rgba[i], rgba[i+1], rgba[i+2], rgba[i+3] = data[i+2], data[i+1], data[i], data[i+3]
	}
	s.blit(dst, rgba)
	return nil
}
