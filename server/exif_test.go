package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// ===========================================================================
// Fixture builders
//
// The parser has to be fed real JPEG bytes, so these helpers assemble a
// minimal EXIF APP1 segment by hand and splice it in behind the SOI marker of
// an ordinary encoded JPEG. That is exactly where a camera puts it, and it
// keeps the fixtures a few dozen bytes rather than checked-in binaries.
// ===========================================================================

func putU16(order binary.ByteOrder, v uint16) []byte {
	var b [2]byte
	order.PutUint16(b[:], v)
	return b[:]
}

func putU32(order binary.ByteOrder, v uint32) []byte {
	var b [4]byte
	order.PutUint32(b[:], v)
	return b[:]
}

type ifdEntry struct {
	tag       uint16
	fieldType uint16 // 3 = SHORT, 4 = LONG
	count     uint32
	value     uint32
}

// exifTIFF builds the little TIFF file that lives inside an EXIF APP1 segment:
// an 8-byte header followed by IFD0 with the given entries.
func exifTIFF(order binary.ByteOrder, entries ...ifdEntry) []byte {
	tiff := []byte("II")
	if order == binary.ByteOrder(binary.BigEndian) {
		tiff = []byte("MM")
	}
	tiff = append(tiff, putU16(order, 42)...)
	tiff = append(tiff, putU32(order, 8)...) // IFD0 sits right after the header
	tiff = append(tiff, putU16(order, uint16(len(entries)))...)
	for _, e := range entries {
		tiff = append(tiff, putU16(order, e.tag)...)
		tiff = append(tiff, putU16(order, e.fieldType)...)
		tiff = append(tiff, putU32(order, e.count)...)
		if e.fieldType == 3 && e.count == 1 {
			// A short value is left-justified in the 4-byte value field.
			tiff = append(tiff, putU16(order, uint16(e.value))...)
			tiff = append(tiff, 0, 0)
		} else {
			tiff = append(tiff, putU32(order, e.value)...)
		}
	}
	return append(tiff, putU32(order, 0)...) // no IFD1
}

// orientationTIFF is the common case: IFD0 carrying only the orientation tag.
func orientationTIFF(order binary.ByteOrder, orientation int) []byte {
	return exifTIFF(order, ifdEntry{tag: 0x0112, fieldType: 3, count: 1, value: uint32(orientation)})
}

// app1 wraps a payload in an APP1 marker segment.
func app1(payload []byte) []byte {
	seg := []byte{0xFF, 0xE1}
	seg = append(seg, putU16(binary.BigEndian, uint16(len(payload)+2))...)
	return append(seg, payload...)
}

// exifAPP1 wraps a TIFF block in the "Exif\0\0"-identified APP1 segment.
func exifAPP1(tiff []byte) []byte {
	return app1(append([]byte("Exif\x00\x00"), tiff...))
}

// jpegWithSegments encodes img and inserts the given marker segments directly
// after the SOI.
func jpegWithSegments(t *testing.T, img image.Image, segments ...[]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	body := buf.Bytes()
	if len(body) < 2 || body[0] != 0xFF || body[1] != 0xD8 {
		t.Fatal("jpeg.Encode did not start with an SOI marker")
	}

	out := append([]byte{}, body[:2]...)
	for _, s := range segments {
		out = append(out, s...)
	}
	return append(out, body[2:]...)
}

// orientedJPEG is a 1x1 JPEG tagged with the given orientation - enough to
// exercise the parser without caring about pixels.
func orientedJPEG(t *testing.T, order binary.ByteOrder, orientation int) []byte {
	t.Helper()
	return jpegWithSegments(t, solidRGBA(1, 1, color.RGBA{9, 9, 9, 255}),
		exifAPP1(orientationTIFF(order, orientation)))
}

func writeTempBytes(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// ===========================================================================
// exifOrientation - the APP1/TIFF parser
// ===========================================================================

func TestExifOrientationAllValues(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for want := 1; want <= 8; want++ {
			data := orientedJPEG(t, order, want)
			if got := exifOrientation(bytes.NewReader(data)); got != want {
				t.Errorf("%v orientation %d: got %d", order, want, got)
			}
		}
	}
}

func TestExifOrientationLongFieldType(t *testing.T) {
	// Rare, but a LONG-typed orientation is well formed and must be read.
	tiff := exifTIFF(binary.LittleEndian, ifdEntry{tag: 0x0112, fieldType: 4, count: 1, value: 6})
	data := jpegWithSegments(t, solidRGBA(1, 1, color.RGBA{1, 2, 3, 255}), exifAPP1(tiff))
	if got := exifOrientation(bytes.NewReader(data)); got != 6 {
		t.Errorf("got %d, want 6", got)
	}
}

func TestExifOrientationFindsTagAmongOthers(t *testing.T) {
	// IFD0 normally holds Make/Model/resolution entries too, and tags are only
	// conventionally sorted - the walk must not stop at the first mismatch.
	tiff := exifTIFF(binary.BigEndian,
		ifdEntry{tag: 0x010F, fieldType: 3, count: 1, value: 1}, // Make
		ifdEntry{tag: 0x0110, fieldType: 3, count: 1, value: 2}, // Model
		ifdEntry{tag: 0x0112, fieldType: 3, count: 1, value: 8}, // Orientation
		ifdEntry{tag: 0x0128, fieldType: 3, count: 1, value: 2}, // ResolutionUnit
	)
	data := jpegWithSegments(t, solidRGBA(1, 1, color.RGBA{1, 2, 3, 255}), exifAPP1(tiff))
	if got := exifOrientation(bytes.NewReader(data)); got != 8 {
		t.Errorf("got %d, want 8", got)
	}
}

func TestExifOrientationSkipsNonExifAPP1(t *testing.T) {
	// Phones routinely write an XMP APP1 ahead of, or instead of, the EXIF one.
	xmp := app1(append([]byte("http://ns.adobe.com/xap/1.0/\x00"), []byte("<x:xmpmeta/>")...))
	data := jpegWithSegments(t, solidRGBA(1, 1, color.RGBA{1, 2, 3, 255}),
		xmp, exifAPP1(orientationTIFF(binary.LittleEndian, 7)))
	if got := exifOrientation(bytes.NewReader(data)); got != 7 {
		t.Errorf("got %d, want 7 (the EXIF APP1 behind an XMP APP1)", got)
	}

	// XMP alone carries no orientation.
	only := jpegWithSegments(t, solidRGBA(1, 1, color.RGBA{1, 2, 3, 255}), xmp)
	if got := exifOrientation(bytes.NewReader(only)); got != 1 {
		t.Errorf("XMP-only JPEG: got %d, want 1", got)
	}
}

func TestExifOrientationPlainJPEGIsUpright(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, gradientRGBA(16, 8), nil); err != nil {
		t.Fatal(err)
	}
	if got := exifOrientation(bytes.NewReader(buf.Bytes())); got != 1 {
		t.Errorf("got %d, want 1 for a JPEG with no EXIF", got)
	}
}

// PNG and GIF have no EXIF at all; the parser must reject them at the SOI
// check instead of misreading their headers as JPEG segments.
func TestExifOrientationNonJPEGFormats(t *testing.T) {
	var pngBuf, gifBuf bytes.Buffer
	if err := png.Encode(&pngBuf, gradientRGBA(8, 8)); err != nil {
		t.Fatal(err)
	}
	if err := gif.Encode(&gifBuf, gradientRGBA(8, 8), nil); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"png": pngBuf.Bytes(), "gif": gifBuf.Bytes()} {
		if got := exifOrientation(bytes.NewReader(data)); got != 1 {
			t.Errorf("%s: got %d, want 1", name, got)
		}
	}
}

// Anything malformed must fall back to 1 rather than erroring out or panicking:
// a photo with damaged metadata should still be shown, just unrotated.
func TestExifOrientationMalformedFallsBackToUpright(t *testing.T) {
	pixel := solidRGBA(1, 1, color.RGBA{1, 2, 3, 255})

	badMagic := orientationTIFF(binary.LittleEndian, 6)
	badMagic[2], badMagic[3] = 0xAA, 0xAA // not 42

	badOrder := orientationTIFF(binary.LittleEndian, 6)
	badOrder[0], badOrder[1] = 'X', 'Y'

	insideHeader := orientationTIFF(binary.LittleEndian, 6)
	copy(insideHeader[4:8], putU32(binary.LittleEndian, 4)) // IFD offset inside the header

	pastEnd := orientationTIFF(binary.LittleEndian, 6)
	copy(pastEnd[4:8], putU32(binary.LittleEndian, 0xFFFFFF))

	overlongCount := orientationTIFF(binary.LittleEndian, 6)
	copy(overlongCount[8:10], putU16(binary.LittleEndian, 500)) // claims 500 entries

	wrongFieldType := exifTIFF(binary.LittleEndian,
		ifdEntry{tag: 0x0112, fieldType: 2, count: 1, value: 6}) // ASCII

	wrongCount := exifTIFF(binary.LittleEndian,
		ifdEntry{tag: 0x0112, fieldType: 3, count: 4, value: 6})

	cases := map[string][]byte{
		"empty":              nil,
		"not a jpeg":         []byte("this is not an image at all"),
		"soi only":           {0xFF, 0xD8},
		"truncated segment":  {0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x40, 'E', 'x'},
		"zero length":        {0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x00},
		"garbage after soi":  {0xFF, 0xD8, 0x12, 0x34, 0x56},
		"exif but no tiff":   jpegWithSegments(t, pixel, exifAPP1(nil)),
		"tiff header cut":    jpegWithSegments(t, pixel, exifAPP1([]byte("II*\x00"))),
		"bad tiff magic":     jpegWithSegments(t, pixel, exifAPP1(badMagic)),
		"bad byte order":     jpegWithSegments(t, pixel, exifAPP1(badOrder)),
		"ifd inside header":  jpegWithSegments(t, pixel, exifAPP1(insideHeader)),
		"ifd past end":       jpegWithSegments(t, pixel, exifAPP1(pastEnd)),
		"ifd count too big":  jpegWithSegments(t, pixel, exifAPP1(overlongCount)),
		"wrong field type":   jpegWithSegments(t, pixel, exifAPP1(wrongFieldType)),
		"wrong value count":  jpegWithSegments(t, pixel, exifAPP1(wrongCount)),
		"no orientation tag": jpegWithSegments(t, pixel, exifAPP1(exifTIFF(binary.LittleEndian))),
	}

	for name, data := range cases {
		if got := exifOrientation(bytes.NewReader(data)); got != 1 {
			t.Errorf("%s: got %d, want 1", name, got)
		}
	}
}

// Values outside 1-8 are meaningless; treating them as an index into a
// transform table would be the classic way to crash on a hostile file.
func TestExifOrientationOutOfRangeValues(t *testing.T) {
	for _, v := range []int{0, 9, 42, 255, 65535} {
		data := orientedJPEG(t, binary.LittleEndian, v)
		if got := exifOrientation(bytes.NewReader(data)); got != 1 {
			t.Errorf("orientation value %d: got %d, want the upright fallback 1", v, got)
		}
	}
}

// countingReader records how much of its source was actually consumed.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// A file made of nothing but 0xFF padding, or of segments that claim to be huge
// and never yield EXIF, must not turn into an unbounded read. The scan budget
// is what has to end both, so this checks the bytes consumed rather than just
// that the call returns.
func TestExifOrientationBoundsPathologicalInput(t *testing.T) {
	const oversized = 8 << 20 // comfortably past maxSegmentScan

	padding := append([]byte{0xFF, 0xD8}, bytes.Repeat([]byte{0xFF}, oversized)...)

	declared := []byte{0xFF, 0xD8}
	for len(declared) < oversized {
		declared = append(declared, 0xFF, 0xE2)
		declared = append(declared, putU16(binary.BigEndian, 0xFFFF)...)
		declared = append(declared, make([]byte, 0xFFFF-2)...)
	}

	for name, data := range map[string][]byte{"0xff padding": padding, "declared segments": declared} {
		counter := &countingReader{r: bytes.NewReader(data)}
		if got := exifOrientation(counter); got != 1 {
			t.Errorf("%s: got %d, want 1", name, got)
		}
		// bufio reads ahead a block at a time, so allow a little slack over the
		// budget - just not the whole file.
		if limit := maxSegmentScan + (1 << 16); counter.n > limit {
			t.Errorf("%s: read %d bytes of a %d byte file, want at most %d",
				name, counter.n, len(data), limit)
		}
	}
}

// ===========================================================================
// applyExifOrientation - the geometry
// ===========================================================================

// Each orientation maps a destination pixel back to a source pixel; markerImage
// stores those source coordinates in R and G, so the mapping can be checked
// exactly.
func TestApplyExifOrientationGeometry(t *testing.T) {
	const w, h = 5, 3

	cases := []struct {
		orientation int
		wantW       int
		wantH       int
		src         func(x, y int) (int, int)
	}{
		{1, w, h, func(x, y int) (int, int) { return x, y }},
		{2, w, h, func(x, y int) (int, int) { return w - 1 - x, y }},
		{3, w, h, func(x, y int) (int, int) { return w - 1 - x, h - 1 - y }},
		{4, w, h, func(x, y int) (int, int) { return x, h - 1 - y }},
		{5, h, w, func(x, y int) (int, int) { return y, x }},
		{6, h, w, func(x, y int) (int, int) { return y, h - 1 - x }},
		{7, h, w, func(x, y int) (int, int) { return w - 1 - y, h - 1 - x }},
		{8, h, w, func(x, y int) (int, int) { return w - 1 - y, x }},
	}

	for _, c := range cases {
		got := applyExifOrientation(markerImage(w, h), c.orientation)
		if got.Bounds() != image.Rect(0, 0, c.wantW, c.wantH) {
			t.Errorf("orientation %d: bounds %v, want (0,0)-(%d,%d)",
				c.orientation, got.Bounds(), c.wantW, c.wantH)
			continue
		}
		for y := 0; y < c.wantH; y++ {
			for x := 0; x < c.wantW; x++ {
				wantX, wantY := c.src(x, y)
				px := color.RGBAModel.Convert(got.At(x, y)).(color.RGBA)
				if int(px.R) != wantX || int(px.G) != wantY {
					t.Fatalf("orientation %d: pixel (%d,%d) came from (%d,%d), want (%d,%d)",
						c.orientation, x, y, px.R, px.G, wantX, wantY)
				}
			}
		}
	}
}

// Orientation 1 is by far the common case; copying a 12 MP decode to return it
// unchanged is the cost this avoids.
func TestApplyExifOrientationUprightReturnsSource(t *testing.T) {
	src := markerImage(4, 3)
	for _, o := range []int{1, 0, 9, -3} {
		if got := applyExifOrientation(src, o); got != image.Image(src) {
			t.Errorf("orientation %d should return the source image itself", o)
		}
	}
}

func TestApplyExifOrientationDoesNotMutateSource(t *testing.T) {
	for o := 1; o <= 8; o++ {
		src := markerImage(5, 4)
		before := append([]byte(nil), src.Pix...)
		applyExifOrientation(src, o)
		if !bytes.Equal(before, src.Pix) {
			t.Fatalf("orientation %d mutated its source image", o)
		}
	}
}

// bestcrop.go narrows the image with SubImage, so every rotated result has to
// stay a concrete *image.RGBA rather than a generic image.Image.
func TestApplyExifOrientationReturnsRGBA(t *testing.T) {
	for o := 2; o <= 8; o++ {
		if _, ok := applyExifOrientation(markerImage(4, 3), o).(*image.RGBA); !ok {
			t.Errorf("orientation %d did not return an *image.RGBA", o)
		}
	}
}

// A sub-image (Bounds().Min != 0) must be read inside its own rectangle.
func TestApplyExifOrientationNonZeroOrigin(t *testing.T) {
	src := image.NewRGBA(image.Rect(3, 3, 8, 6)) // 5x3 anchored at (3,3)
	src.SetRGBA(3, 3, color.RGBA{1, 2, 3, 255})  // top-left of the source

	got := applyExifOrientation(src, 6) // rotate 90 clockwise
	if got.Bounds() != image.Rect(0, 0, 3, 5) {
		t.Fatalf("got %v, want a 3x5 image at the origin", got.Bounds())
	}
	// Rotating clockwise sends the source's top-left to the result's top-right.
	if c := got.At(2, 0).(color.RGBA); c.R != 1 || c.G != 2 || c.B != 3 {
		t.Errorf("top-right pixel %v, want the source's top-left {1 2 3 255}", c)
	}
}

// ===========================================================================
// ReadImage - end to end, through a real JPEG decode
// ===========================================================================

// quadrantImage paints four clearly separated gray levels, one per quadrant.
// Gray survives a JPEG round trip well because luminance is never chroma
// subsampled, which is what lets the orientation be identified after decode.
func quadrantImage(w, h int, levels [2][2]uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := levels[2*y/h][2*x/w]
			img.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	return img
}

// quadrantLevels samples the centre of each quadrant of img.
func quadrantLevels(img image.Image) [2][2]uint8 {
	b := img.Bounds()
	var out [2][2]uint8
	for row := 0; row < 2; row++ {
		for col := 0; col < 2; col++ {
			x := b.Min.X + b.Dx()*(1+2*col)/4
			y := b.Min.Y + b.Dy()*(1+2*row)/4
			r, _, _, _ := img.At(x, y).RGBA()
			out[row][col] = uint8(r >> 8)
		}
	}
	return out
}

// ReadImage must hand the pipeline an upright image: this walks all eight
// orientations through a genuine JPEG encode/decode and checks both the
// dimensions and which quadrant ended up where.
func TestReadImageAppliesExifOrientation(t *testing.T) {
	const w, h = 64, 32
	const a, b, c, d uint8 = 0, 85, 170, 255
	stored := [2][2]uint8{{a, b}, {c, d}} // as laid out in the file

	cases := []struct {
		orientation int
		wantW       int
		wantH       int
		want        [2][2]uint8
	}{
		{1, w, h, [2][2]uint8{{a, b}, {c, d}}}, // as stored
		{2, w, h, [2][2]uint8{{b, a}, {d, c}}}, // mirrored horizontally
		{3, w, h, [2][2]uint8{{d, c}, {b, a}}}, // rotated 180
		{4, w, h, [2][2]uint8{{c, d}, {a, b}}}, // mirrored vertically
		{5, h, w, [2][2]uint8{{a, c}, {b, d}}}, // transposed
		{6, h, w, [2][2]uint8{{c, a}, {d, b}}}, // rotated 90 clockwise
		{7, h, w, [2][2]uint8{{d, b}, {c, a}}}, // transverse
		{8, h, w, [2][2]uint8{{b, d}, {a, c}}}, // rotated 90 counter-clockwise
	}

	dir := t.TempDir()
	for _, tc := range cases {
		data := jpegWithSegments(t, quadrantImage(w, h, stored),
			exifAPP1(orientationTIFF(binary.LittleEndian, tc.orientation)))
		path := writeTempBytes(t, dir, "o.jpg", data)

		got, err := ReadImage(path)
		if err != nil {
			t.Fatalf("orientation %d: ReadImage: %v", tc.orientation, err)
		}
		if got.Bounds().Dx() != tc.wantW || got.Bounds().Dy() != tc.wantH {
			t.Fatalf("orientation %d: got %v, want a %dx%d image",
				tc.orientation, got.Bounds(), tc.wantW, tc.wantH)
		}

		levels := quadrantLevels(got)
		for row := 0; row < 2; row++ {
			for col := 0; col < 2; col++ {
				// JPEG is lossy; the levels are 85 apart so a wide tolerance
				// still identifies the quadrant unambiguously.
				diff := int(levels[row][col]) - int(tc.want[row][col])
				if diff < -20 || diff > 20 {
					t.Fatalf("orientation %d: quadrants %v, want %v", tc.orientation, levels, tc.want)
				}
			}
		}
	}
}

// The orientation scan reads from the file before the decode does; the rewind
// in between has to leave the decoder looking at the SOI.
func TestReadImageDecodesAfterOrientationScan(t *testing.T) {
	dir := t.TempDir()
	src := gradientRGBA(16, 16)
	data := jpegWithSegments(t, src, exifAPP1(orientationTIFF(binary.BigEndian, 1)))
	got, err := ReadImage(writeTempBytes(t, dir, "a.jpg", data))
	if err != nil {
		t.Fatalf("ReadImage: %v", err)
	}
	if got.Bounds().Dx() != 16 || got.Bounds().Dy() != 16 {
		t.Fatalf("got %v, want a 16x16 image", got.Bounds())
	}
}

// Damaged metadata must cost the rotation, not the photo.
func TestReadImageMalformedExifStillDecodes(t *testing.T) {
	dir := t.TempDir()
	broken := orientationTIFF(binary.LittleEndian, 6)
	broken[2], broken[3] = 0x00, 0x00 // clobber the TIFF magic

	data := jpegWithSegments(t, gradientRGBA(24, 12), exifAPP1(broken))
	got, err := ReadImage(writeTempBytes(t, dir, "a.jpg", data))
	if err != nil {
		t.Fatalf("ReadImage: %v", err)
	}
	if got.Bounds().Dx() != 24 || got.Bounds().Dy() != 12 {
		t.Fatalf("got %v, want the un-rotated 24x12 image", got.Bounds())
	}
}

// PNG and GIF carry no EXIF and must come back byte-identical.
func TestReadImageNonJPEGPassesThroughUnchanged(t *testing.T) {
	dir := t.TempDir()
	src := gradientRGBA(12, 5)

	for _, tc := range []struct {
		name string
		path string
	}{
		{"png", writeTempPNG(t, dir, "a.png", src)},
		{"gif", writeTempGIF(t, dir, "a.gif", src)},
	} {
		got, err := ReadImage(tc.path)
		if err != nil {
			t.Fatalf("%s: ReadImage: %v", tc.name, err)
		}
		if got.Bounds().Dx() != 12 || got.Bounds().Dy() != 5 {
			t.Fatalf("%s: got %v, want a 12x5 image", tc.name, got.Bounds())
		}
	}

	// The PNG round trip is lossless, so every pixel must match exactly.
	got, err := ReadImage(filepath.Join(dir, "a.png"))
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 5; y++ {
		for x := 0; x < 12; x++ {
			if got.At(x, y) != src.At(x, y) {
				t.Fatalf("png pixel (%d,%d) changed: %v vs %v", x, y, got.At(x, y), src.At(x, y))
			}
		}
	}
}
