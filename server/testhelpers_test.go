package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestMain silences the standard logger: chi's Logger middleware and the
// handlers' own diagnostics would otherwise bury the test output.
func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Palettes
//
// These mirror the color spaces the real firmware sends (the palettes are
// declared per driver and serialised by
// esphome/components/eink_frame/eink_frame.cpp -> get_image_request_body()).
// Keeping them in sync means the tests exercise the same data the server
// actually receives.
// ---------------------------------------------------------------------------

// epd7in3fPalette is the 7-color Waveshare EPD7IN3F color space.
// Codes: 0 black, 1 white, 2 green, 3 blue, 4 red, 5 yellow, 6 orange.
var epd7in3fPalette = ColorSpace{
	{Colorf{0, 0, 0}, 0},
	{Colorf{1, 1, 1}, 1},
	{Colorf{0.059, 0.329, 0.119}, 2},
	{Colorf{0.061, 0.147, 0.336}, 3},
	{Colorf{0.574, 0.066, 0.010}, 4},
	{Colorf{0.982, 0.756, 0.004}, 5},
	{Colorf{0.795, 0.255, 0.018}, 6},
}

// el133uf1Palette is the 6-color Spectra 6 color space (note the gap: no code 4).
var el133uf1Palette = ColorSpace{
	{Colorf{0, 0, 0}, 0},
	{Colorf{1, 1, 1}, 1},
	{Colorf{0.982, 0.756, 0.004}, 2},
	{Colorf{0.574, 0.066, 0.010}, 3},
	{Colorf{0.061, 0.147, 0.336}, 5},
	{Colorf{0.059, 0.329, 0.119}, 6},
}

// grey16Palette is the IT8951 16-level grayscale color space.
func grey16Palette() ColorSpace {
	var cs ColorSpace
	for i := 0; i < 16; i++ {
		v := float64(i) / 15.0
		cs = append(cs, ColorSpace{{Colorf{v, v, v}, uint8(i)}}...)
	}
	return cs
}

// bwPalette is a minimal two-entry black/white color space, useful for
// reasoning about dithering by hand.
var bwPalette = ColorSpace{
	{Colorf{0, 0, 0}, 0},
	{Colorf{1, 1, 1}, 1},
}

// firmwareRequestBody is the exact JSON the EPD7IN3F driver sends: the palette
// comes from esphome/components/epd7in3f/, serialised by
// esphome/components/eink_frame/eink_frame.cpp. Used to pin the wire format.
const firmwareRequestBody = `{"width":800,"height":480,"flip_vertical":false,"flip_horizonal":true,` +
	`"color_space":[` +
	`{"color_code":0,"rgb_color":[0,0,0]},` +
	`{"color_code":1,"rgb_color":[1,1,1]},` +
	`{"color_code":2,"rgb_color":[0.059,0.329,0.119]},` +
	`{"color_code":3,"rgb_color":[0.061,0.147,0.336]},` +
	`{"color_code":4,"rgb_color":[0.574,0.066,0.010]},` +
	`{"color_code":5,"rgb_color":[0.982,0.756,0.004]},` +
	`{"color_code":6,"rgb_color":[0.795,0.255,0.018]}]}`

// ---------------------------------------------------------------------------
// Numeric helpers
// ---------------------------------------------------------------------------

const eps = 1e-9

func almostEqual(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func assertColorf(t *testing.T, got, want Colorf, tol float64, msg string) {
	t.Helper()
	for i := 0; i < 3; i++ {
		if !almostEqual(got[i], want[i], tol) {
			t.Errorf("%s: got %v, want %v (component %d differs by %g)", msg, got, want, i, math.Abs(got[i]-want[i]))
			return
		}
	}
}

// inverseGamma returns the color that CorrectGamma maps onto c. Feeding
// inverseGamma(paletteColor) into the pipeline makes the gamma-corrected pixel
// land exactly on paletteColor, which is what lets us assert "a palette color
// maps to itself".
func inverseGamma(c Colorf) Colorf {
	return Colorf{
		math.Pow(c[0], 1.0/1.2),
		math.Pow(c[1], 1.0/1.2),
		math.Pow(c[2], 1.0/1.2),
	}
}

func colorfToRGBA8(c Colorf) color.RGBA {
	round := func(v float64) uint8 {
		q := v*255 + 0.5
		if q < 0 {
			q = 0
		}
		if q > 255 {
			q = 255
		}
		return uint8(q)
	}
	return color.RGBA{round(c[0]), round(c[1]), round(c[2]), 255}
}

// ---------------------------------------------------------------------------
// Image builders
// ---------------------------------------------------------------------------

// solidRGBA builds a w x h *image.RGBA filled with c.
func solidRGBA(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// gradientRGBA builds a deterministic, non-uniform test image.
func gradientRGBA(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8((x * 255) / maxInt(w-1, 1)),
				G: uint8((y * 255) / maxInt(h-1, 1)),
				B: uint8(((x + y) * 255) / maxInt(w+h-2, 1)),
				A: 255,
			})
		}
	}
	return img
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// writeTempPNG encodes img as a PNG inside dir and returns the path.
func writeTempPNG(t *testing.T, dir, name string, img image.Image) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create %s: %v", p, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return p
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func writeTempJPEG(t *testing.T, dir, name string, img image.Image) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create %s: %v", p, err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return p
}

func writeTempGIF(t *testing.T, dir, name string, img image.Image) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create %s: %v", p, err)
	}
	defer f.Close()
	if err := gif.Encode(f, img, nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return p
}

// ---------------------------------------------------------------------------
// smartcrop-cli stub
//
// bestcrop.go shells out to the external `smartcrop-cli` Python program. It is
// not available in the test environment (and we must not depend on Python), so
// these helpers put a tiny shell script named `smartcrop-cli` on PATH. That
// lets the full fetchImage pipeline be exercised end-to-end without touching
// production code.
// ---------------------------------------------------------------------------

type smartcropStub struct {
	dir     string
	argsLog string // file the stub appends its argv to
}

// stubSmartcrop installs a fake smartcrop-cli that prints the given crop
// rectangle as JSON. Returns the stub so tests can inspect the recorded argv.
func stubSmartcrop(t *testing.T, x, y, w, h int) *smartcropStub {
	t.Helper()
	return stubSmartcropRaw(t, jsonCrop(x, y, w, h), 0)
}

func jsonCrop(x, y, w, h int) string {
	b, _ := json.Marshal(PythonResult{X: x, Y: y, Width: w, Height: h})
	return string(b)
}

// stubSmartcropRaw installs a fake smartcrop-cli that prints stdout verbatim and
// exits with exitCode.
func stubSmartcropRaw(t *testing.T, stdout string, exitCode int) *smartcropStub {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub is POSIX only")
	}
	dir := t.TempDir()
	argsLog := filepath.Join(dir, "args.log")
	payload := filepath.Join(dir, "payload.json")
	if err := os.WriteFile(payload, []byte(stdout), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> '" + argsLog + "'\n" +
		"cat '" + payload + "'\n" +
		"exit " + strconv.Itoa(exitCode) + "\n"
	bin := filepath.Join(dir, "smartcrop-cli")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &smartcropStub{dir: dir, argsLog: argsLog}
}

func (s *smartcropStub) recordedArgs(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(s.argsLog)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
		out = append(out, string(line))
	}
	return out
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

// newTestRouter serves the production router, middleware and all, so that route
// or middleware drift is a test failure rather than something the tests
// re-declare for themselves.
func newTestRouter() *chi.Mux {
	return newRouter()
}

func decodeJSON(body string, v interface{}) error {
	return json.NewDecoder(bytes.NewReader([]byte(body))).Decode(v)
}

func mustJSON(t *testing.T, v interface{}) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return bytes.NewReader(b)
}

// withImageDir points the package-level imageDir at dir for the duration of the
// test.
func withImageDir(t *testing.T, dir string) {
	t.Helper()
	old := imageDir
	imageDir = dir
	t.Cleanup(func() { imageDir = old })
}

// silenceStdout redirects os.Stdout for the duration of the test; the handlers
// are chatty with fmt.Printf.
func silenceStdout(t *testing.T) {
	t.Helper()
	old := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	os.Stdout = devnull
	t.Cleanup(func() {
		os.Stdout = old
		devnull.Close()
	})
}
