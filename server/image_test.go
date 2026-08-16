package main

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ===========================================================================
// Colorf arithmetic
// ===========================================================================

func TestAddColorf(t *testing.T) {
	got := addColorf(Colorf{0.1, 0.2, 0.3}, Colorf{0.4, -0.1, 1.0})
	assertColorf(t, got, Colorf{0.5, 0.1, 1.3}, 1e-12, "addColorf")
}

func TestSubtractColorf(t *testing.T) {
	got := subtractColorf(Colorf{0.5, 0.2, 0.3}, Colorf{0.1, 0.4, 0.3})
	assertColorf(t, got, Colorf{0.4, -0.2, 0.0}, 1e-12, "subtractColorf")

	// Subtracting a color from itself is the zero error the dither relies on.
	assertColorf(t, subtractColorf(Colorf{0.7, 0.1, 0.9}, Colorf{0.7, 0.1, 0.9}),
		Colorf{0, 0, 0}, 1e-12, "self subtraction")
}

func TestMultColor(t *testing.T) {
	assertColorf(t, multColor(Colorf{0.2, 0.4, 0.8}, 0.5), Colorf{0.1, 0.2, 0.4}, 1e-12, "multColor")
	assertColorf(t, multColor(Colorf{0.2, 0.4, 0.8}, 0), Colorf{0, 0, 0}, 1e-12, "multColor by zero")
	assertColorf(t, multColor(Colorf{0.2, 0.4, 0.8}, -1), Colorf{-0.2, -0.4, -0.8}, 1e-12, "multColor negative")
}

func TestClamp(t *testing.T) {
	cases := []struct{ v, lo, hi, want float64 }{
		{0.5, 0, 1, 0.5},
		{-0.5, 0, 1, 0},
		{1.5, 0, 1, 1},
		{0, 0, 1, 0},
		{1, 0, 1, 1},
		{-3, -1, 2, -1},
	}
	for _, c := range cases {
		if got := clamp(c.v, c.lo, c.hi); got != c.want {
			t.Errorf("clamp(%g, %g, %g) = %g, want %g", c.v, c.lo, c.hi, got, c.want)
		}
	}
}

func TestClampColor(t *testing.T) {
	got := clampColor(Colorf{-0.5, 0.5, 1.5}, 0, 1)
	assertColorf(t, got, Colorf{0, 0.5, 1}, 1e-12, "clampColor")
}

// ===========================================================================
// CorrectGamma
// ===========================================================================

func TestCorrectGamma(t *testing.T) {
	// The implementation uses a plain power law with gamma 1.2.
	cases := []Colorf{
		{0, 0, 0},
		{1, 1, 1},
		{0.5, 0.25, 0.75},
		{0.1, 0.9, 0.42},
	}
	for _, in := range cases {
		want := Colorf{math.Pow(in[0], 1.2), math.Pow(in[1], 1.2), math.Pow(in[2], 1.2)}
		assertColorf(t, CorrectGamma(in), want, 1e-12, fmt.Sprintf("CorrectGamma(%v)", in))
	}
}

func TestCorrectGammaFixedPoints(t *testing.T) {
	// 0 and 1 must be preserved exactly, otherwise pure black and pure white
	// would stop matching their palette entries.
	assertColorf(t, CorrectGamma(Colorf{0, 0, 0}), Colorf{0, 0, 0}, 0, "gamma(0)")
	assertColorf(t, CorrectGamma(Colorf{1, 1, 1}), Colorf{1, 1, 1}, 0, "gamma(1)")
}

func TestCorrectGammaDarkensMidTones(t *testing.T) {
	// gamma > 1 pulls mid tones down.
	for _, v := range []float64{0.1, 0.25, 0.5, 0.75, 0.9} {
		got := CorrectGamma(Colorf{v, v, v})[0]
		if got >= v {
			t.Errorf("CorrectGamma(%g) = %g; gamma 1.2 should darken mid tones", v, got)
		}
	}
}

func TestCorrectGammaIsMonotonic(t *testing.T) {
	prev := -1.0
	for i := 0; i <= 100; i++ {
		v := float64(i) / 100
		got := CorrectGamma(Colorf{v, v, v})[0]
		if got < prev {
			t.Fatalf("CorrectGamma is not monotonic at %g (%g < %g)", v, got, prev)
		}
		prev = got
	}
}

func TestCorrectGammaAppliesPerChannel(t *testing.T) {
	got := CorrectGamma(Colorf{1, 0, 0.5})
	if got[0] != 1 || got[1] != 0 {
		t.Errorf("channels are not independent: %v", got)
	}
	if !almostEqual(got[2], math.Pow(0.5, 1.2), 1e-12) {
		t.Errorf("blue channel wrong: %v", got)
	}
}

// ===========================================================================
// convertToRGBA
// ===========================================================================

func TestConvertToRGBA(t *testing.T) {
	cases := []struct {
		in   Colorf
		want color.RGBA
	}{
		{Colorf{0, 0, 0}, color.RGBA{0, 0, 0, 255}},
		{Colorf{1, 1, 1}, color.RGBA{255, 255, 255, 255}},
		{Colorf{0.5, 0.5, 0.5}, color.RGBA{127, 127, 127, 255}}, // truncation, not rounding
		{Colorf{1, 0, 0}, color.RGBA{255, 0, 0, 255}},
		{Colorf{0.059, 0.329, 0.119}, color.RGBA{15, 83, 30, 255}},
	}
	for _, c := range cases {
		if got := convertToRGBA(c.in); got != c.want {
			t.Errorf("convertToRGBA(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestConvertToRGBAAlwaysOpaque(t *testing.T) {
	for _, c := range []Colorf{{0, 0, 0}, {1, 1, 1}, {0.3, 0.6, 0.9}} {
		if got := convertToRGBA(c).A; got != 255 {
			t.Errorf("convertToRGBA(%v).A = %d, want 255", c, got)
		}
	}
}

// ===========================================================================
// imageToRGBA / ConvertImageToColors
// ===========================================================================

func TestImageToRGBAPassesThroughRGBA(t *testing.T) {
	src := gradientRGBA(4, 3)
	if got := imageToRGBA(src); got != src {
		t.Error("imageToRGBA should return an *image.RGBA unchanged (same pointer)")
	}
}

func TestImageToRGBAConvertsOtherFormats(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			src.SetGray(x, y, color.Gray{Y: uint8(x * 60)})
		}
	}
	dst := imageToRGBA(src)
	if dst.Bounds() != image.Rect(0, 0, 4, 3) {
		t.Fatalf("got bounds %v, want (0,0)-(4,3)", dst.Bounds())
	}
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			want := uint8(x * 60)
			got := dst.RGBAAt(x, y)
			if got.R != want || got.G != want || got.B != want || got.A != 255 {
				t.Errorf("pixel (%d,%d) = %v, want gray %d opaque", x, y, got, want)
			}
		}
	}
}

func TestImageToRGBANormalisesOriginOfNonRGBA(t *testing.T) {
	src := image.NewNRGBA(image.Rect(5, 7, 9, 10)) // 4x3 at a non-zero origin
	src.SetNRGBA(5, 7, color.NRGBA{10, 20, 30, 255})
	dst := imageToRGBA(src)
	if dst.Bounds() != image.Rect(0, 0, 4, 3) {
		t.Fatalf("got bounds %v, want (0,0)-(4,3)", dst.Bounds())
	}
	if got := dst.RGBAAt(0, 0); got.R != 10 || got.G != 20 || got.B != 30 {
		t.Errorf("top-left pixel not translated to the origin: %v", got)
	}
}

func TestConvertImageToColors(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	src.SetRGBA(0, 0, color.RGBA{0, 0, 0, 255})
	src.SetRGBA(1, 0, color.RGBA{255, 255, 255, 255})
	src.SetRGBA(0, 1, color.RGBA{255, 0, 0, 255})
	src.SetRGBA(1, 1, color.RGBA{0, 128, 255, 255})

	got := ConvertImageToColors(src)
	if len(got) != 2 || len(got[0]) != 2 {
		t.Fatalf("got a %dx%d grid, want 2x2 (rows then columns)", len(got), len(got[0]))
	}
	assertColorf(t, got[0][0], Colorf{0, 0, 0}, 1e-12, "(0,0)")
	assertColorf(t, got[0][1], Colorf{1, 1, 1}, 1e-12, "(1,0)")
	assertColorf(t, got[1][0], Colorf{1, 0, 0}, 1e-12, "(0,1)")
	assertColorf(t, got[1][1], Colorf{0, 128.0 / 255.0, 1}, 1e-3, "(1,1)")
}

func TestConvertImageToColorsIsRowMajor(t *testing.T) {
	// A 4-wide, 2-tall image must give 2 rows of 4, not 4 rows of 2.
	got := ConvertImageToColors(gradientRGBA(4, 2))
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	for i, row := range got {
		if len(row) != 4 {
			t.Fatalf("row %d has %d columns, want 4", i, len(row))
		}
	}
}

func TestConvertImageToColorsRangeIsUnitInterval(t *testing.T) {
	for _, c := range ConvertImageToColors(gradientRGBA(9, 7)) {
		for _, px := range c {
			for i, v := range px {
				if v < 0 || v > 1 {
					t.Fatalf("channel %d out of [0,1]: %g", i, v)
				}
			}
		}
	}
}

// An image whose Bounds().Min is non-zero - exactly what image.SubImage returns,
// as bestcrop.go produces - must be sized from Dx()/Dy() and read with the Min
// offset applied, not indexed from (0,0).
func TestConvertImageToColorsSubImage(t *testing.T) {
	src := gradientRGBA(8, 8)
	sub := src.SubImage(image.Rect(2, 2, 6, 6)).(*image.RGBA) // 4x4
	got := ConvertImageToColors(sub)
	if len(got) != 4 || len(got[0]) != 4 {
		t.Fatalf("got a %dx%d grid, want 4x4", len(got), len(got[0]))
	}
	// Grid cell (0,0) must be the sub-image's own top-left pixel, i.e. (2,2).
	want := ConvertImageToColors(src)[2][2]
	assertColorf(t, got[0][0], want, 1e-12, "sub-image origin should map to grid (0,0)")
}

// ===========================================================================
// flipImage
// ===========================================================================

// markerImage encodes each pixel's coordinates in R and G so flips are easy to
// verify.
func markerImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 7, A: 255})
		}
	}
	return img
}

func TestFlipImageNoFlipIsIdentity(t *testing.T) {
	src := markerImage(4, 3)
	got := flipImage(src, false, false)
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			if got.At(x, y) != src.At(x, y) {
				t.Fatalf("pixel (%d,%d) changed with no flip requested", x, y)
			}
		}
	}
}

func TestFlipImageHorizontal(t *testing.T) {
	const w, h = 4, 3
	got := flipImage(markerImage(w, h), false, true)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := got.At(x, y).(color.RGBA)
			if int(c.R) != w-x-1 || int(c.G) != y {
				t.Fatalf("horizontal flip: pixel (%d,%d) came from (%d,%d), want (%d,%d)",
					x, y, c.R, c.G, w-x-1, y)
			}
		}
	}
}

func TestFlipImageVertical(t *testing.T) {
	const w, h = 4, 3
	got := flipImage(markerImage(w, h), true, false)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := got.At(x, y).(color.RGBA)
			if int(c.R) != x || int(c.G) != h-y-1 {
				t.Fatalf("vertical flip: pixel (%d,%d) came from (%d,%d), want (%d,%d)",
					x, y, c.R, c.G, x, h-y-1)
			}
		}
	}
}

func TestFlipImageBoth(t *testing.T) {
	const w, h = 4, 3
	got := flipImage(markerImage(w, h), true, true)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := got.At(x, y).(color.RGBA)
			if int(c.R) != w-x-1 || int(c.G) != h-y-1 {
				t.Fatalf("180 rotation: pixel (%d,%d) came from (%d,%d), want (%d,%d)",
					x, y, c.R, c.G, w-x-1, h-y-1)
			}
		}
	}
}

func TestFlipImageIsInvolutive(t *testing.T) {
	src := gradientRGBA(7, 5)
	for _, tc := range [][2]bool{{true, false}, {false, true}, {true, true}} {
		once := flipImage(src, tc[0], tc[1])
		twice := flipImage(once, tc[0], tc[1])
		for y := 0; y < 5; y++ {
			for x := 0; x < 7; x++ {
				if twice.At(x, y) != src.At(x, y) {
					t.Fatalf("flip(v=%v,h=%v) applied twice is not the identity at (%d,%d)", tc[0], tc[1], x, y)
				}
			}
		}
	}
}

func TestFlipImagePreservesBoundsAndReturnsRGBA(t *testing.T) {
	src := markerImage(6, 4)
	got := flipImage(src, true, true)
	if got.Bounds() != image.Rect(0, 0, 6, 4) {
		t.Errorf("got bounds %v, want (0,0)-(6,4)", got.Bounds())
	}
	if _, ok := got.(*image.RGBA); !ok {
		t.Errorf("flipImage should return an *image.RGBA (bestcrop.go relies on SubImage), got %T", got)
	}
}

func TestFlipImageDoesNotMutateSource(t *testing.T) {
	src := markerImage(5, 4)
	before := append([]byte(nil), src.Pix...)
	flipImage(src, true, true)
	for i := range before {
		if before[i] != src.Pix[i] {
			t.Fatal("flipImage mutated its source image")
		}
	}
}

// An image anchored away from the origin must be sized from Dx()/Dy() and read
// inside its own rectangle. ReadImage always decodes origin-anchored images, but
// a sub-image is not.
func TestFlipImageNonZeroOrigin(t *testing.T) {
	src := image.NewRGBA(image.Rect(3, 3, 7, 6)) // 4x3 at (3,3)
	src.SetRGBA(3, 3, color.RGBA{1, 2, 3, 255})
	got := flipImage(src, false, false)
	if got.Bounds().Dx() != 4 || got.Bounds().Dy() != 3 {
		t.Fatalf("got %v, want a 4x3 image", got.Bounds())
	}
	if c := got.At(0, 0).(color.RGBA); c.R != 1 || c.G != 2 || c.B != 3 {
		t.Errorf("top-left pixel %v, want the source's (3,3) pixel {1 2 3 255}", c)
	}
}

// ===========================================================================
// ReadImage
// ===========================================================================

func TestReadImagePNG(t *testing.T) {
	dir := t.TempDir()
	src := gradientRGBA(6, 4)
	path := writeTempPNG(t, dir, "a.png", src)

	got, err := ReadImage(path)
	if err != nil {
		t.Fatalf("ReadImage: %v", err)
	}
	if got.Bounds() != image.Rect(0, 0, 6, 4) {
		t.Fatalf("got bounds %v, want (0,0)-(6,4)", got.Bounds())
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 6; x++ {
			r1, g1, b1, _ := got.At(x, y).RGBA()
			r2, g2, b2, _ := src.At(x, y).RGBA()
			if r1 != r2 || g1 != g2 || b1 != b2 {
				t.Fatalf("pixel (%d,%d) round-tripped as (%d,%d,%d), want (%d,%d,%d)", x, y, r1, g1, b1, r2, g2, b2)
			}
		}
	}
}

func TestReadImageJPEG(t *testing.T) {
	dir := t.TempDir()
	path := writeTempJPEG(t, dir, "a.jpg", solidRGBA(8, 8, color.RGBA{200, 100, 50, 255}))
	got, err := ReadImage(path)
	if err != nil {
		t.Fatalf("ReadImage: %v", err)
	}
	if got.Bounds().Dx() != 8 || got.Bounds().Dy() != 8 {
		t.Fatalf("got %v, want an 8x8 image", got.Bounds())
	}
}

func TestReadImageGIF(t *testing.T) {
	dir := t.TempDir()
	path := writeTempGIF(t, dir, "a.gif", solidRGBA(8, 8, color.RGBA{0, 0, 255, 255}))
	got, err := ReadImage(path)
	if err != nil {
		t.Fatalf("ReadImage: %v", err)
	}
	if got.Bounds().Dx() != 8 || got.Bounds().Dy() != 8 {
		t.Fatalf("got %v, want an 8x8 image", got.Bounds())
	}
}

// A decode failure has to be surfaced: a nil image.Image would be dereferenced
// by the very next stage of the pipeline.
func TestReadImageUndecodable(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "notanimage.txt")
	if err := writeFile(bad, "this is not an image"); err != nil {
		t.Fatal(err)
	}

	got, err := ReadImage(bad)
	if err == nil {
		t.Fatal("expected an error for an undecodable file")
	}
	if got != nil {
		t.Errorf("expected no image alongside the error, got %v", got.Bounds())
	}
	if !strings.Contains(err.Error(), "notanimage.txt") {
		t.Errorf("error %q should name the offending file", err)
	}
}

// A file that cannot be opened must fail the one request, not the process:
// ReadImage used to call os.Exit(1) here.
func TestReadImageMissingFile(t *testing.T) {
	got, err := ReadImage(filepath.Join(t.TempDir(), "does-not-exist.png"))
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
	if got != nil {
		t.Errorf("expected no image alongside the error, got %v", got.Bounds())
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error should wrap os.ErrNotExist, got %v", err)
	}
}
