package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"image"
	"io"
)

// EXIF orientation support.
//
// Go's image/jpeg discards metadata, so a phone photo that stores the sensor's
// native pixels and records "turn me 90 degrees" in EXIF tag 0x0112 decodes
// sideways. Everything downstream — smart crop, resize, and the device's own
// flip_vertical/flip_horizonal request — assumes upright input, so the fix
// belongs immediately after the decode, in ReadImage.
//
// The parser below is deliberately self-contained: a dependency would mean
// re-vendoring the Go module tree and updating the vendorHash in
// server/default.nix for one 16-bit integer. It reads only what it needs (the
// APP1 "Exif\0\0" segment, the TIFF header, and IFD0) and treats every
// malformed, truncated or unrecognised input as orientation 1 (upright), which
// makes the whole feature a no-op for anything that is not an oriented JPEG.

// errNoOrientation is the single sentinel for "there is no usable orientation
// here". Callers never distinguish the reasons; they all mean "assume upright".
var errNoOrientation = errors.New("no EXIF orientation")

// maxSegmentScan bounds how much of a file the segment walk will read before
// giving up. EXIF lives in the first few segments of a JPEG; a file that has
// not produced one within a megabyte of headers is not worth chasing (and a
// hostile file must not be able to make us read forever).
const maxSegmentScan = 1 << 20

// exifOrientation returns the EXIF orientation (1-8) of the JPEG in r, or 1 if
// r is not a JPEG, carries no EXIF, or the metadata is malformed. It reads from
// r's current position and consumes an unspecified amount of it, so callers
// that also want to decode the image must rewind afterwards.
func exifOrientation(r io.Reader) int {
	tiff, err := findExifSegment(bufio.NewReader(r))
	if err != nil {
		return 1
	}
	orientation, err := tiffOrientation(tiff)
	if err != nil || orientation < 1 || orientation > 8 {
		return 1
	}
	return orientation
}

// findExifSegment walks the JPEG marker segments and returns the body of the
// first APP1 segment that carries the "Exif\0\0" identifier, i.e. the bytes
// starting at the TIFF header.
func findExifSegment(r *bufio.Reader) ([]byte, error) {
	var soi [2]byte
	if _, err := io.ReadFull(r, soi[:]); err != nil {
		return nil, errNoOrientation
	}
	if soi[0] != 0xFF || soi[1] != 0xD8 {
		return nil, errNoOrientation // not a JPEG: PNG and GIF land here
	}

	scanned := 2
	for scanned < maxSegmentScan {
		marker, n, err := nextMarker(r, maxSegmentScan-scanned)
		scanned += n
		if err != nil {
			return nil, err
		}

		switch {
		case marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7):
			// Standalone markers: no length, no payload.
			continue
		case marker == 0xDA || marker == 0xD9:
			// Start of scan / end of image. Metadata cannot follow, and past
			// SOS the entropy-coded data is not marker-structured at all.
			return nil, errNoOrientation
		}

		var lengthBytes [2]byte
		if _, err := io.ReadFull(r, lengthBytes[:]); err != nil {
			return nil, errNoOrientation
		}
		length := int(binary.BigEndian.Uint16(lengthBytes[:]))
		if length < 2 {
			return nil, errNoOrientation // the length includes its own 2 bytes
		}
		scanned += length
		payloadLen := length - 2

		// APP1 also carries XMP, so a non-Exif APP1 is skipped like any other
		// segment rather than ending the search.
		if marker != 0xE1 {
			if _, err := io.CopyN(io.Discard, r, int64(payloadLen)); err != nil {
				return nil, errNoOrientation
			}
			continue
		}

		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, errNoOrientation
		}
		if len(payload) >= 6 && string(payload[:6]) == "Exif\x00\x00" {
			return payload[6:], nil
		}
	}

	return nil, errNoOrientation
}

// nextMarker reads the 0xFF-prefixed marker that starts the next segment and
// reports how many bytes that took, so the caller can charge them to its scan
// budget. It reads at most budget bytes.
func nextMarker(r *bufio.Reader, budget int) (byte, int, error) {
	consumed := 0

	b, err := r.ReadByte()
	if err != nil {
		return 0, consumed, errNoOrientation
	}
	consumed++
	if b != 0xFF {
		return 0, consumed, errNoOrientation // we are not at a segment boundary
	}

	// Any number of extra 0xFF bytes may pad the gap between segments, so they
	// are skipped rather than taken for the marker. The budget is what stops a
	// file that is nothing but padding from being read all the way to its end.
	for consumed < budget {
		b, err = r.ReadByte()
		if err != nil {
			return 0, consumed, errNoOrientation
		}
		consumed++
		if b != 0xFF {
			return b, consumed, nil
		}
	}

	return 0, consumed, errNoOrientation
}

// tiffOrientation parses the little TIFF file that an EXIF APP1 segment
// contains and returns the value of IFD0's orientation tag (0x0112). Only IFD0
// is walked: the orientation tag is defined to live there, and the sub-IFDs it
// points at are irrelevant.
func tiffOrientation(tiff []byte) (int, error) {
	if len(tiff) < 8 {
		return 0, errNoOrientation
	}

	var order binary.ByteOrder
	switch {
	case tiff[0] == 'I' && tiff[1] == 'I':
		order = binary.LittleEndian
	case tiff[0] == 'M' && tiff[1] == 'M':
		order = binary.BigEndian
	default:
		return 0, errNoOrientation
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 0, errNoOrientation // TIFF magic
	}

	// All offsets are relative to the start of the TIFF header. The header
	// itself is 8 bytes, so anything pointing inside it is malformed.
	offset := int(order.Uint32(tiff[4:8]))
	if offset < 8 || offset > len(tiff)-2 {
		return 0, errNoOrientation
	}

	count := int(order.Uint16(tiff[offset : offset+2]))
	entries := tiff[offset+2:]
	if count*12 > len(entries) {
		return 0, errNoOrientation // truncated IFD
	}

	for i := 0; i < count; i++ {
		entry := entries[i*12 : i*12+12]
		if order.Uint16(entry[0:2]) != 0x0112 {
			continue
		}
		if order.Uint32(entry[4:8]) != 1 {
			return 0, errNoOrientation // orientation is a single value
		}
		// A SHORT or LONG value of count 1 fits in the 4-byte value field, so
		// it is stored inline and never needs another offset chase.
		switch order.Uint16(entry[2:4]) {
		case 3: // SHORT
			return int(order.Uint16(entry[8:10])), nil
		case 4: // LONG
			return int(order.Uint32(entry[8:12])), nil
		}
		return 0, errNoOrientation
	}

	return 0, errNoOrientation
}

// applyExifOrientation turns a decoded image into the upright image the
// photographer saw, given the EXIF orientation of the file it came from.
//
// Orientation 1 (and any value this package refuses to interpret) returns the
// source untouched — the pipeline's largest image must not be copied
// pixel-by-pixel just to stay identical.
func applyExifOrientation(img image.Image, orientation int) image.Image {
	switch orientation {
	case 2: // mirrored horizontally
		return flipImage(img, false, true)
	case 3: // rotated 180 degrees
		return flipImage(img, true, true)
	case 4: // mirrored vertically
		return flipImage(img, true, false)
	case 5, 6, 7, 8:
		return transposeImage(img, orientation)
	default:
		return img
	}
}

// transposeImage handles the four EXIF orientations whose axes are swapped, so
// a w x h source becomes an h x w result.
func transposeImage(img image.Image, orientation int) image.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < w; y++ {
		for x := 0; x < h; x++ {
			var srcX, srcY int
			switch orientation {
			case 5: // mirrored along the main diagonal (transpose)
				srcX, srcY = y, x
			case 6: // rotated 90 degrees clockwise
				srcX, srcY = y, h-1-x
			case 7: // mirrored along the anti-diagonal (transverse)
				srcX, srcY = w-1-y, h-1-x
			default: // 8: rotated 90 degrees counter-clockwise
				srcX, srcY = w-1-y, x
			}
			dst.Set(x, y, img.At(bounds.Min.X+srcX, bounds.Min.Y+srcY))
		}
	}

	return dst
}
