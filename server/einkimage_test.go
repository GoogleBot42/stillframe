package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/mattn/go-ciede2000"
)

// ===========================================================================
// packBytesIntoNibbles
// ===========================================================================

func TestPackBytesIntoNibbles(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want []byte
	}{
		{"empty", []byte{}, nil},
		{"nil", nil, nil},
		{"single pair", []byte{0x1, 0x2}, []byte{0x12}},
		{"two pairs", []byte{0x1, 0x2, 0x3, 0x4}, []byte{0x12, 0x34}},
		{"zeroes", []byte{0x0, 0x0}, []byte{0x00}},
		{"max nibbles", []byte{0xF, 0xF}, []byte{0xFF}},
		{"first pixel is high nibble", []byte{0xA, 0x0}, []byte{0xA0}},
		{"second pixel is low nibble", []byte{0x0, 0xB}, []byte{0x0B}},
		{
			"full 16-gray ramp",
			[]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
			[]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := packBytesIntoNibbles(tc.in)
			if !bytes.Equal(got, tc.want) {
				t.Errorf("packBytesIntoNibbles(%#v) = %#v, want %#v", tc.in, got, tc.want)
			}
			if len(tc.in)%2 == 0 && len(got) != len(tc.in)/2 {
				t.Errorf("expected 2 pixels per byte: len(in)=%d len(out)=%d", len(tc.in), len(got))
			}
		})
	}
}

// An odd pixel count (e.g. a 3x3 request) leaves a half-full trailing byte. The
// last pixel goes in the high nibble and the low nibble is padded with 0, so no
// caller can be made to read out of range.
func TestPackBytesIntoNibblesOddLengthPads(t *testing.T) {
	got := packBytesIntoNibbles([]byte{0x1, 0x2, 0x3})
	want := []byte{0x12, 0x30}
	if !bytes.Equal(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

// Color codes wider than 4 bits are silently truncated in the high nibble
// (input[x]<<4 on a byte drops the top 4 bits) but preserved in the low nibble.
// No shipped palette uses codes > 15, but the asymmetry is worth pinning.
func TestPackBytesIntoNibblesTruncatesHighNibble(t *testing.T) {
	got := packBytesIntoNibbles([]byte{0x1F, 0x1F})
	want := []byte{0xFF} // high: (0x1F<<4)&0xFF = 0xF0, low: 0x1F -> 0xFF after OR
	if !bytes.Equal(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
	// The low nibble is NOT masked, so an out-of-range code corrupts the byte:
	got = packBytesIntoNibbles([]byte{0x0, 0x1F})
	if got[0] != 0x1F {
		t.Errorf("low nibble is unmasked; got %#v, want 0x1f", got)
	}
}

// ===========================================================================
// min
// ===========================================================================

func TestMin(t *testing.T) {
	cases := [][3]int{{1, 2, 1}, {2, 1, 1}, {3, 3, 3}, {-5, 2, -5}, {0, -1, -1}}
	for _, c := range cases {
		if got := min(c[0], c[1]); got != c[2] {
			t.Errorf("min(%d, %d) = %d, want %d", c[0], c[1], got, c[2])
		}
	}
}

// ===========================================================================
// ditherFloydSteinberg
// ===========================================================================

func newColorfGrid(w, h int, fill float64) [][]Colorf {
	g := make([][]Colorf, h)
	for y := range g {
		g[y] = make([]Colorf, w)
		for x := range g[y] {
			g[y][x] = Colorf{fill, fill, fill}
		}
	}
	return g
}

func TestDitherFloydSteinbergWeights(t *testing.T) {
	// A 3x2 grid, diffusing from the middle of the top row so all four
	// neighbours are in bounds.
	grid := newColorfGrid(3, 2, 0)
	err := Colorf{0.16, 0.32, 0.64}
	ditherFloydSteinberg(grid, 1, 0, err)

	for _, e := range []struct {
		x, y   int
		weight float64
	}{
		{2, 0, 7.0 / 16.0}, // right
		{0, 1, 3.0 / 16.0}, // below-left
		{1, 1, 5.0 / 16.0}, // below
		{2, 1, 1.0 / 16.0}, // below-right
	} {
		assertColorf(t, grid[e.y][e.x], multColor(err, e.weight), eps,
			fmt.Sprintf("neighbour (%d,%d) should receive %g of the error", e.x, e.y, e.weight))
	}

	// The source pixel itself must be untouched.
	assertColorf(t, grid[0][1], Colorf{0, 0, 0}, eps, "source pixel must not be modified")
	// (0,0) is not a Floyd-Steinberg neighbour of (1,0).
	assertColorf(t, grid[0][0], Colorf{0, 0, 0}, eps, "pixel to the left must not be modified")
}

func TestDitherFloydSteinbergWeightsSumToOne(t *testing.T) {
	grid := newColorfGrid(3, 2, 0)
	ditherFloydSteinberg(grid, 1, 0, Colorf{1, 0, 0})
	total := grid[0][2][0] + grid[1][0][0] + grid[1][1][0] + grid[1][2][0]
	if !almostEqual(total, 1.0, eps) {
		t.Errorf("Floyd-Steinberg weights should sum to 1, distributed %g", total)
	}
}

func TestDitherFloydSteinbergSkipsOutOfBounds(t *testing.T) {
	// Bottom-right corner: every neighbour is out of bounds, nothing may change.
	grid := newColorfGrid(2, 2, 0.25)
	ditherFloydSteinberg(grid, 1, 1, Colorf{1, 1, 1})
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			assertColorf(t, grid[y][x], Colorf{0.25, 0.25, 0.25}, eps, "corner diffusion must be a no-op")
		}
	}

	// Left edge: the x-1 neighbour must be skipped, not wrapped to the right edge.
	grid = newColorfGrid(3, 2, 0)
	ditherFloydSteinberg(grid, 0, 0, Colorf{1, 1, 1})
	assertColorf(t, grid[1][2], Colorf{0, 0, 0}, eps, "x-1 must not wrap around to the last column")
	assertColorf(t, grid[1][0], multColor(Colorf{1, 1, 1}, 5.0/16.0), eps, "below")
	assertColorf(t, grid[1][1], multColor(Colorf{1, 1, 1}, 1.0/16.0), eps, "below-right")
	assertColorf(t, grid[0][1], multColor(Colorf{1, 1, 1}, 7.0/16.0), eps, "right")
}

func TestDitherFloydSteinbergClamps(t *testing.T) {
	grid := newColorfGrid(2, 2, 0.9)
	ditherFloydSteinberg(grid, 0, 0, Colorf{1, 1, 1}) // 0.9 + 7/16 > 1
	assertColorf(t, grid[0][1], Colorf{1, 1, 1}, eps, "positive overflow must clamp to 1")

	grid = newColorfGrid(2, 2, 0.05)
	ditherFloydSteinberg(grid, 0, 0, Colorf{-1, -1, -1})
	assertColorf(t, grid[0][1], Colorf{0, 0, 0}, eps, "negative overflow must clamp to 0")
}

// ===========================================================================
// ConvertToEInkImage
// ===========================================================================

func TestConvertToEInkImageOutputSize(t *testing.T) {
	for _, dim := range [][2]int{{2, 1}, {8, 4}, {4, 4}, {16, 10}, {800, 4}} {
		w, h := dim[0], dim[1]
		got := ConvertToEInkImage(gradientRGBA(w, h), epd7in3fPalette)
		if want := w * h / 2; len(got) != want {
			t.Errorf("%dx%d: got %d bytes, want %d (2 pixels/byte)", w, h, len(got), want)
		}
	}
}

// Exact palette colors must map to themselves. The pipeline gamma-corrects the
// source pixel before matching, so the source pixel is pre-compensated with the
// inverse gamma; after CorrectGamma it lands exactly on the palette entry.
func TestConvertToEInkImageExactPaletteColorsMapToThemselves(t *testing.T) {
	for _, palette := range []ColorSpace{epd7in3fPalette, el133uf1Palette, grey16Palette()} {
		for _, def := range palette {
			// 2x1 image: only pixel 0 is asserted, so accumulated dither error
			// from earlier pixels can never influence the result.
			img := image.NewRGBA(image.Rect(0, 0, 2, 1))
			img.SetRGBA(0, 0, colorfToRGBA8(inverseGamma(def.Color)))
			img.SetRGBA(1, 0, color.RGBA{0, 0, 0, 255})

			out := ConvertToEInkImage(img, palette)
			if len(out) != 1 {
				t.Fatalf("expected 1 byte, got %d", len(out))
			}
			if got := out[0] >> 4; got != def.Code {
				t.Errorf("palette color %v (code %d) mapped to code %d instead of itself", def.Color, def.Code, got)
			}
		}
	}
}

// Spot-check that CIEDE2000 picks the perceptually obvious palette entry for a
// handful of unambiguous inputs.
func TestConvertToEInkImageNearestColorSpotChecks(t *testing.T) {
	tests := []struct {
		name string
		in   color.RGBA
		want uint8
	}{
		{"pure black -> black", color.RGBA{0, 0, 0, 255}, 0},
		{"pure white -> white", color.RGBA{255, 255, 255, 255}, 1},
		{"near black -> black", color.RGBA{8, 8, 8, 255}, 0},
		{"near white -> white", color.RGBA{250, 250, 250, 255}, 1},
		{"pure blue -> blue", color.RGBA{0, 0, 255, 255}, 3},
		{"navy -> blue", color.RGBA{20, 40, 110, 255}, 3},
		{"pure yellow -> yellow", color.RGBA{255, 255, 0, 255}, 5},
		{"gold -> yellow", color.RGBA{250, 200, 10, 255}, 5},
		{"orange -> orange", color.RGBA{255, 128, 0, 255}, 6},
		{"dark red -> red", color.RGBA{150, 20, 5, 255}, 4},
		{"dark forest green -> green", color.RGBA{20, 90, 35, 255}, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 2, 1))
			img.SetRGBA(0, 0, tc.in)
			img.SetRGBA(1, 0, tc.in)
			out := ConvertToEInkImage(img, epd7in3fPalette)
			if got := out[0] >> 4; got != tc.want {
				t.Errorf("%v mapped to code %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// Characterisation test, not an endorsement: CIEDE2000 weights lightness
// heavily, and the EPD7IN3F "green" primary is very dark (L* ~ 35) while sRGB
// pure green is very light (L* ~ 88). Pure green therefore matches *white*, and
// pure red matches orange rather than the darker red primary. This is the
// algorithm working as specified given the palette, but it surprises people, so
// it is pinned here.
func TestConvertToEInkImageBrightPrimariesFavourLightness(t *testing.T) {
	cases := []struct {
		name string
		in   color.RGBA
		want uint8
	}{
		{"sRGB pure green matches white, not the dark green primary", color.RGBA{0, 255, 0, 255}, 1},
		{"sRGB pure red matches orange, not the dark red primary", color.RGBA{255, 0, 0, 255}, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 2, 1))
			img.SetRGBA(0, 0, tc.in)
			img.SetRGBA(1, 0, tc.in)
			out := ConvertToEInkImage(img, epd7in3fPalette)
			if got := out[0] >> 4; got != tc.want {
				t.Errorf("%v mapped to code %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestConvertToEInkImageUniformBlackAndWhite(t *testing.T) {
	// Black and white are gamma fixed points and dither to zero error, so a
	// solid image must produce an exactly uniform buffer.
	black := ConvertToEInkImage(solidRGBA(8, 4, color.RGBA{0, 0, 0, 255}), epd7in3fPalette)
	for i, b := range black {
		if b != 0x00 {
			t.Fatalf("solid black byte %d = %#x, want 0x00", i, b)
		}
	}
	white := ConvertToEInkImage(solidRGBA(8, 4, color.RGBA{255, 255, 255, 255}), epd7in3fPalette)
	for i, b := range white {
		if b != 0x11 {
			t.Fatalf("solid white byte %d = %#x, want 0x11", i, b)
		}
	}
}

// A mid-gray on a pure black/white palette can only be represented by mixing
// codes; the dither must produce both values rather than a flat field.
func TestConvertToEInkImageDithersMidTones(t *testing.T) {
	out := ConvertToEInkImage(solidRGBA(16, 8, color.RGBA{190, 190, 190, 255}), bwPalette)
	var black, white int
	for _, b := range out {
		for _, nib := range []byte{b >> 4, b & 0xF} {
			if nib == 0 {
				black++
			} else {
				white++
			}
		}
	}
	if black == 0 || white == 0 {
		t.Fatalf("expected a mix of black and white from dithering, got black=%d white=%d", black, white)
	}
	// Error diffusion must preserve average intensity: the fraction of white
	// pixels should track the gamma-corrected input value.
	// (190/255) ** 1.2, computed independently of CorrectGamma so the assertion
	// is not self-referential.
	const wantWhiteFraction = 0.70252

	gotWhiteFraction := float64(white) / float64(black+white)
	if !almostEqual(gotWhiteFraction, wantWhiteFraction, 0.06) {
		t.Errorf("dither should average out to the source intensity: got %.3f white, want ~%.3f (black=%d white=%d)",
			gotWhiteFraction, wantWhiteFraction, black, white)
	}
}

func TestConvertToEInkImageIsDeterministic(t *testing.T) {
	img := gradientRGBA(32, 16)
	first := ConvertToEInkImage(img, epd7in3fPalette)
	for i := 0; i < 5; i++ {
		again := ConvertToEInkImage(img, epd7in3fPalette)
		if !bytes.Equal(first, again) {
			t.Fatalf("ConvertToEInkImage is not deterministic (run %d differs)", i)
		}
	}
}

func TestConvertToEInkImageDoesNotMutateSource(t *testing.T) {
	img := gradientRGBA(16, 8)
	before := make([]byte, len(img.Pix))
	copy(before, img.Pix)
	ConvertToEInkImage(img, epd7in3fPalette)
	if !bytes.Equal(before, img.Pix) {
		t.Error("ConvertToEInkImage mutated its source image")
	}
}

// The error diffused into the neighbouring pixels must actually change the
// output: a gradient encoded with dithering differs from what a pure
// nearest-color quantiser would emit.
func TestConvertToEInkImageDitheringAffectsNeighbours(t *testing.T) {
	// Every pixel is the same mid-gray. Without error diffusion the output
	// would be a constant byte; with it, it must not be.
	out := ConvertToEInkImage(solidRGBA(16, 8, color.RGBA{128, 128, 128, 255}), bwPalette)
	uniform := true
	for _, b := range out[1:] {
		if b != out[0] {
			uniform = false
			break
		}
	}
	if uniform {
		t.Error("expected error diffusion to break up a flat mid-gray field")
	}
}

// No shipped geometry has an odd pixel count, but a request for one must pad
// rather than panic.
func TestConvertToEInkImageOddPixelCount(t *testing.T) {
	got := ConvertToEInkImage(gradientRGBA(3, 3), epd7in3fPalette)
	if len(got) != 5 {
		t.Errorf("3x3 image: got %d bytes, want 5 (ceil(9/2))", len(got))
	}
}

// The size must come from bounds.Dx()/Dy() with reads offset by bounds.Min, so
// that a sub-image (what bestcrop.go's SubImage call produces) is walked over
// its own rectangle rather than from the origin.
func TestConvertToEInkImageSubImage(t *testing.T) {
	src := gradientRGBA(16, 16)
	sub := src.SubImage(image.Rect(4, 4, 12, 12)) // 8x8
	got := ConvertToEInkImage(sub, epd7in3fPalette)
	if want := 8 * 8 / 2; len(got) != want {
		t.Errorf("8x8 sub-image: got %d bytes, want %d", len(got), want)
	}
}

// ===========================================================================
// GenerateCalibrationImage
// ===========================================================================

func mustCalibrationImage(t *testing.T, w, h int, palette ColorSpace) []byte {
	t.Helper()
	got, err := GenerateCalibrationImage(w, h, palette)
	if err != nil {
		t.Fatalf("GenerateCalibrationImage(%d, %d): %v", w, h, err)
	}
	return got
}

func mustClearImage(t *testing.T, w, h int, palette ColorSpace) []byte {
	t.Helper()
	got, err := GenerateClearImage(w, h, palette)
	if err != nil {
		t.Fatalf("GenerateClearImage(%d, %d): %v", w, h, err)
	}
	return got
}

func TestGenerateCalibrationImageFourColorGrid(t *testing.T) {
	// 4 colors -> sideLength 2 -> a 2x2 grid of quadrants over a 4x4 image.
	palette := ColorSpace{
		{Colorf{0, 0, 0}, 0},
		{Colorf{1, 1, 1}, 1},
		{Colorf{1, 0, 0}, 2},
		{Colorf{0, 0, 1}, 3},
	}
	got := mustCalibrationImage(t, 4, 4, palette)
	want := []byte{
		0x00, 0x11, // y=0: codes 0,0,1,1
		0x00, 0x11, // y=1
		0x22, 0x33, // y=2: codes 2,2,3,3
		0x22, 0x33, // y=3
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestGenerateCalibrationImageSizeAndCoverage(t *testing.T) {
	const w, h = 60, 36
	got := mustCalibrationImage(t, w, h, epd7in3fPalette)
	if want := w * h / 2; len(got) != want {
		t.Fatalf("got %d bytes, want %d", len(got), want)
	}

	seen := map[byte]bool{}
	for _, b := range got {
		seen[b>>4] = true
		seen[b&0xF] = true
	}
	for _, def := range epd7in3fPalette {
		if !seen[def.Code] {
			t.Errorf("calibration image never shows color code %d", def.Code)
		}
	}
	if len(seen) != len(epd7in3fPalette) {
		t.Errorf("calibration image contains unexpected codes: %v", seen)
	}
}

// With 7 colors the grid is 3x3, so the two trailing cells are clamped onto the
// last color rather than reading past the palette.
func TestGenerateCalibrationImageClampsTrailingCells(t *testing.T) {
	got := mustCalibrationImage(t, 6, 6, epd7in3fPalette)
	// Bottom-right pixel is row 2, column 2 -> index min(6, 8) = 6 -> code 6.
	last := got[len(got)-1]
	if last&0xF != 6 {
		t.Errorf("bottom-right cell should clamp to the last color code 6, got %d", last&0xF)
	}
	// Middle-right cell (row 1, col 2) -> index min(6, 5) = 5 -> code 5.
	// Row y=2 (row index 1), x=5 -> byte index (2*6+5)/2 = 8, low nibble.
	if got[8]&0xF != 5 {
		t.Errorf("row 1 col 2 should be code 5, got %d", got[8]&0xF)
	}
}

func TestGenerateCalibrationImageGrey16(t *testing.T) {
	// 16 colors -> sideLength 4.
	got := mustCalibrationImage(t, 64, 32, grey16Palette())
	if want := 64 * 32 / 2; len(got) != want {
		t.Fatalf("got %d bytes, want %d", len(got), want)
	}
	// Top-left is code 0, top-right is code 3 (row 0, column 3).
	if got[0]>>4 != 0 {
		t.Errorf("top-left should be code 0, got %d", got[0]>>4)
	}
	if got[31]&0xF != 3 {
		t.Errorf("top-right should be code 3, got %d", got[31]&0xF)
	}
}

func TestGenerateCalibrationImageZeroSize(t *testing.T) {
	if got := mustCalibrationImage(t, 0, 0, epd7in3fPalette); got != nil {
		t.Errorf("expected nil for a 0x0 image, got %#v", got)
	}
}

// A panel smaller than the color grid (height 2 < sideLength 3) used to divide
// by zero; the spacings clamp to one pixel per cell instead.
func TestGenerateCalibrationImageTinyHeight(t *testing.T) {
	if got := mustCalibrationImage(t, 6, 2, epd7in3fPalette); len(got) != 6 {
		t.Errorf("got %d bytes, want 6", len(got))
	}
}

func TestGenerateCalibrationImageEmptyColorSpace(t *testing.T) {
	got, err := GenerateCalibrationImage(8, 4, ColorSpace{})
	if err == nil {
		t.Fatalf("expected an error for an empty color space, got %#v", got)
	}
	if got != nil {
		t.Errorf("expected no data alongside the error, got %#v", got)
	}
}

// ===========================================================================
// GenerateClearImage
// ===========================================================================

func TestGenerateClearImageIsUniform(t *testing.T) {
	const w, h = 12, 6
	got := mustClearImage(t, w, h, epd7in3fPalette)
	if want := w * h / 2; len(got) != want {
		t.Fatalf("got %d bytes, want %d", len(got), want)
	}
	for i, b := range got {
		if b != got[0] {
			t.Fatalf("clear image is not uniform: byte %d = %#x, byte 0 = %#x", i, b, got[0])
		}
	}
	// Both nibbles of the byte must be the same code.
	if got[0]>>4 != got[0]&0xF {
		t.Errorf("clear image byte %#x has mismatched nibbles", got[0])
	}
}

// The clear color is the palette entry closest to white. That index differs per
// panel (1 on the color panels, 15 on the 16-gray one), so it must be derived
// from the palette rather than hard-coded.
func TestGenerateClearImageIsWhite(t *testing.T) {
	cases := []struct {
		name    string
		palette ColorSpace
		want    uint8
	}{
		{"epd7in3f", epd7in3fPalette, 1},
		{"el133uf1", el133uf1Palette, 1},
		{"grey16", grey16Palette(), 15},
		{"black and white", bwPalette, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mustClearImage(t, 4, 2, tc.palette)
			want := tc.want<<4 | tc.want
			for _, b := range got {
				if b != want {
					t.Fatalf("got %#x, want %#x (white)", b, want)
				}
			}
		})
	}
}

// A palette with fewer than four entries used to index out of range.
func TestGenerateClearImageSmallPalette(t *testing.T) {
	got := mustClearImage(t, 4, 2, bwPalette)
	if len(got) != 4 {
		t.Fatalf("got %d bytes, want 4", len(got))
	}
}

func TestGenerateClearImageEmptyColorSpace(t *testing.T) {
	got, err := GenerateClearImage(8, 4, ColorSpace{})
	if err == nil {
		t.Fatalf("expected an error for an empty color space, got %#v", got)
	}
	if got != nil {
		t.Errorf("expected no data alongside the error, got %#v", got)
	}
}

func TestGenerateClearImageZeroSize(t *testing.T) {
	if got := mustClearImage(t, 0, 0, epd7in3fPalette); got != nil {
		t.Errorf("expected nil for a 0x0 image, got %#v", got)
	}
}

// ===========================================================================
// paletteMatcher
// ===========================================================================

// The matcher is a pure optimization: for every representable 8-bit input it
// must pick the same entry as the unmemoized per-call form it replaced (Diff on
// two RGBA values, i.e. an sRGB->Lab conversion of both arguments every call).
func TestPaletteMatcherMatchesUnmemoizedDiff(t *testing.T) {
	// Deterministic LCG (Numerical Recipes constants) so the samples spread
	// through the whole 8-bit cube rather than along a periodic lattice.
	state := uint32(1)
	nextChannel := func() float64 {
		state = state*1664525 + 1013904223
		return float64(state>>24) / 255
	}

	for _, palette := range []ColorSpace{epd7in3fPalette, el133uf1Palette, grey16Palette(), bwPalette} {
		matcher := newPaletteMatcher(palette)
		for i := 0; i < 4500; i++ {
			target := Colorf{nextChannel(), nextChannel(), nextChannel()}

			wantCode, wantColor := func() (uint8, Colorf) {
				minDist := math.MaxFloat64
				code, best := uint8(0), Colorf{0, 0, 0}
				for _, def := range palette {
					if dist := ciede2000.Diff(convertToRGBA(target), convertToRGBA(def.Color)); dist < minDist {
						minDist = dist
						code, best = def.Code, def.Color
					}
				}
				return code, best
			}()

			gotCode, gotColor := matcher.nearest(target)
			if gotCode != wantCode || gotColor != wantColor {
				t.Fatalf("matcher.nearest(%v) = code %d %v, brute force = code %d %v",
					target, gotCode, gotColor, wantCode, wantColor)
			}
			// The second query answers from the cache and must agree.
			if code, c := matcher.nearest(target); code != gotCode || c != gotColor {
				t.Fatalf("cached matcher.nearest(%v) = code %d %v, first answer was code %d %v",
					target, code, c, gotCode, gotColor)
			}
		}
	}
}

// Repeated queries must come from the cache without drifting, including targets
// that only differ below 8-bit precision (same truncated key).
func TestPaletteMatcherCacheIsStable(t *testing.T) {
	matcher := newPaletteMatcher(epd7in3fPalette)
	firstCode, firstColor := matcher.nearest(Colorf{0.5, 0.25, 0.75})
	for i := 0; i < 3; i++ {
		code, c := matcher.nearest(Colorf{0.5 + 1e-9, 0.25, 0.75})
		if code != firstCode || c != firstColor {
			t.Fatalf("repeat query drifted: got code %d %v, want %d %v", code, c, firstCode, firstColor)
		}
	}
}

func TestPaletteMatcherEmptyColorSpace(t *testing.T) {
	code, c := newPaletteMatcher(ColorSpace{}).nearest(Colorf{0.9, 0.9, 0.9})
	if code != 0 || c != (Colorf{0, 0, 0}) {
		t.Errorf("empty color space should yield black/code 0, got code %d %v", code, c)
	}
	code, c = nearestPaletteColor(ColorSpace{}, Colorf{0.9, 0.9, 0.9})
	if code != 0 || c != (Colorf{0, 0, 0}) {
		t.Errorf("nearestPaletteColor on an empty space should yield black/code 0, got code %d %v", code, c)
	}
}

// The device-timeout-relevant path: quantize+dither a photo-like gradient at
// panel resolution. Issue #15's before/after on this benchmark shape is
// ~24 s -> ~0.8 s for grey16 1872x1404 with a 12 MP source; here the source is
// already panel-sized so only the quantization cost is visible.
func BenchmarkConvertToEInkImageGrey16(b *testing.B) {
	img := gradientRGBA(1872, 1404)
	palette := grey16Palette()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ConvertToEInkImage(img, palette)
	}
}

func BenchmarkConvertToEInkImageColor7(b *testing.B) {
	img := gradientRGBA(800, 480)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ConvertToEInkImage(img, epd7in3fPalette)
	}
}

// ===========================================================================
// ColorSpace JSON wire format
// ===========================================================================

func TestColorSpaceDecodesFirmwareBody(t *testing.T) {
	var props ImageProperties
	if err := decodeJSON(firmwareRequestBody, &props); err != nil {
		t.Fatalf("decoding the real firmware body failed: %v", err)
	}
	if props.Width != 800 || props.Height != 480 {
		t.Errorf("got %dx%d, want 800x480", props.Width, props.Height)
	}
	if props.FlipVertical {
		t.Error("flip_vertical should be false")
	}
	if !props.FlipHorizonal {
		t.Error("flip_horizonal should be true (note the firmware/server share the typo)")
	}
	if len(props.ColorSpace) != 7 {
		t.Fatalf("got %d colors, want 7", len(props.ColorSpace))
	}
	for i, def := range props.ColorSpace {
		if int(def.Code) != i {
			t.Errorf("color %d has code %d", i, def.Code)
		}
		assertColorf(t, def.Color, epd7in3fPalette[i].Color, 1e-12, "decoded palette entry")
	}
}
