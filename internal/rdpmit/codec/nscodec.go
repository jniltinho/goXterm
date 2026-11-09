package codec

import "encoding/binary"

// NSCodec decoder (MS-RDPNSC). rustxterm: rewritten — upstream's RLE was not the NSCodec scheme
// and it treated Co/Cg as unsigned, so every Windows 11 tile failed to decode. This follows the
// spec (3.1.9 RLE, 3.1.8.1 colour) as FreeRDP's nsc_rle_decode / nsc_decode implement it.

// nscRLEDecode expands one RLE plane to size bytes: a value followed by the same value is a run
// (next byte + 2, or 0xFF then a 32-bit length); the last 4 bytes are stored raw.
func nscRLEDecode(in []byte, size int) []byte {
	out := make([]byte, 0, size+4)
	left, i := size, 0
	for left > 4 {
		if i >= len(in) {
			return nil
		}
		v := in[i]
		i++
		if left == 5 || i >= len(in) || in[i] != v {
			out = append(out, v)
			left--
			continue
		}
		i++ // the repeated value
		if i >= len(in) {
			return nil
		}
		n := int(in[i]) + 2
		i++
		if in[i-1] == 0xFF {
			if i+4 > len(in) {
				return nil
			}
			n = int(binary.LittleEndian.Uint32(in[i:]))
			i += 4
		}
		if n > left {
			return nil
		}
		for k := 0; k < n; k++ {
			out = append(out, v)
		}
		left -= n
	}
	if i+4 > len(in) {
		return nil
	}
	return append(out, in[i:i+4]...)[:size]
}

// nscPlane returns a plane of size bytes: absent (count 0) is all 0xFF, shorter than size is RLE,
// otherwise raw.
func nscPlane(data []byte, size int) []byte {
	switch {
	case len(data) == 0:
		p := make([]byte, size)
		for i := range p {
			p[i] = 0xFF
		}
		return p
	case len(data) < size:
		return nscRLEDecode(data, size)
	default:
		return data[:size]
	}
}

// DecodeNSCodecToRGBA decodes an NSCodec bitmap stream (MS-RDPNSC 2.2.2), whose rows are stored
// bottom-up in surface bits, to top-down RGBA.
func DecodeNSCodecToRGBA(data []byte, width, height int) []byte {
	return decodeNSCodec(data, width, height, true)
}

// DecodeNSCodec decodes an NSCodec bitmap stream stored top-down with its alpha plane (ClearCodec's
// NSCodec subcodec) to RGBA (rustxterm addition).
func DecodeNSCodec(data []byte, width, height int) []byte {
	return decodeNSCodec(data, width, height, false)
}

func decodeNSCodec(data []byte, width, height int, surfaceBits bool) []byte {
	if len(data) < 20 || width <= 0 || height <= 0 {
		return nil
	}
	var count [4]int
	for i := range count {
		count[i] = int(binary.LittleEndian.Uint32(data[4*i:]))
	}
	colorLoss, subsampling := data[16], data[17] != 0
	if colorLoss < 1 || colorLoss > 7 {
		return nil
	}
	rw := roundUpToMultiple(width, 8) // luma stride with subsampling
	size := [4]int{width * height, width * height, width * height, width * height}
	if subsampling {
		size[0] = rw * height
		size[1] = (rw / 2) * (roundUpToMultiple(height, 2) / 2)
		size[2] = size[1]
	}
	var planes [4][]byte
	off := 20
	for i := range planes {
		if count[i] < 0 || off+count[i] > len(data) {
			return nil
		}
		if planes[i] = nscPlane(data[off:off+count[i]], size[i]); planes[i] == nil {
			return nil
		}
		off += count[i]
	}
	shift := colorLoss - 1
	rgba := make([]byte, width*height*4)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			yi, ci := y*width+x, y*width+x
			if subsampling {
				yi, ci = y*rw+x, (y/2)*(rw/2)+x/2
			}
			yv := int(planes[0][yi])
			co := int(int8(planes[1][ci] << shift))
			cg := int(int8(planes[2][ci] << shift))
			o := (y*width + x) * 4
			a := planes[3][y*width+x]
			if surfaceBits {
				o = ((height-1-y)*width + x) * 4 // rows are bottom-up (FreeRDP: FREERDP_FLIP_VERTICAL)
				a = 255                          // desktop alpha is not used (as with planar bitmaps)
			}
			rgba[o] = clampByteNS(yv + co - cg)
			rgba[o+1] = clampByteNS(yv + cg)
			rgba[o+2] = clampByteNS(yv - co - cg)
			rgba[o+3] = a
		}
	}
	return rgba
}

// clampByteNS clamps a value to 0-255.
func clampByteNS(v int) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}
