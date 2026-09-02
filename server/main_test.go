package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// ===========================================================================
// ImageProperties decoding
// ===========================================================================

func TestImagePropertiesJSONTags(t *testing.T) {
	body := `{"width":1872,"height":1404,"flip_vertical":true,"flip_horizonal":false,
	          "color_space":[{"color_code":9,"rgb_color":[0.25,0.5,0.75]}]}`
	var props ImageProperties
	if err := decodeJSON(body, &props); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if props.Width != 1872 || props.Height != 1404 {
		t.Errorf("got %dx%d", props.Width, props.Height)
	}
	if !props.FlipVertical || props.FlipHorizonal {
		t.Errorf("flips decoded wrong: v=%v h=%v", props.FlipVertical, props.FlipHorizonal)
	}
	if len(props.ColorSpace) != 1 || props.ColorSpace[0].Code != 9 {
		t.Fatalf("color space decoded wrong: %+v", props.ColorSpace)
	}
	assertColorf(t, props.ColorSpace[0].Color, Colorf{0.25, 0.5, 0.75}, 1e-12, "rgb_color")
}

func TestImagePropertiesMissingFieldsDefaultToZero(t *testing.T) {
	var props ImageProperties
	if err := decodeJSON(`{}`, &props); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if props.Width != 0 || props.Height != 0 || props.FlipVertical || props.FlipHorizonal || props.ColorSpace != nil {
		t.Errorf("expected the zero value, got %+v", props)
	}
}

// ===========================================================================
// Handlers
// ===========================================================================

func postJSON(t *testing.T, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	router := newTestRouter()
	req := httptest.NewRequest(http.MethodPost, path, mustJSON(t, body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func postRaw(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := newTestRouter()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestCalibrationImageHandler(t *testing.T) {
	const w, h = 40, 24
	rec := postJSON(t, "/calibrationImage", ImageProperties{Width: w, Height: h, ColorSpace: epd7in3fPalette})

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	body := rec.Body.Bytes()
	if want := w * h / 2; len(body) != want {
		t.Errorf("got %d bytes, want %d (width*height/2)", len(body), want)
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(body)) {
		t.Errorf("Content-Length %q does not match the %d bytes written", got, len(body))
	}
}

// Every successful response is nibble-packed panel data, and none of it is
// text. Left unset, net/http sniffs the body — a bright 16-gray frame really
// does come back text/plain; charset=utf-8 — which invites any proxy in front
// of this server to rewrite or recompress the bytes the firmware clocks
// straight out to the panel.
func TestHandlersDeclareOctetStream(t *testing.T) {
	imgDir := t.TempDir()
	writeTempPNG(t, imgDir, "only.png", gradientRGBA(64, 48))
	withImageDir(t, imgDir)

	for _, ep := range []string{"/fetchImage", "/calibrationImage", "/clearImage"} {
		t.Run(ep, func(t *testing.T) {
			rec := postJSON(t, ep, ImageProperties{Width: 40, Height: 24, ColorSpace: epd7in3fPalette})
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
				t.Errorf("Content-Type %q, want application/octet-stream", got)
			}
		})
	}
}

// The same, for the palette that provoked the mis-sniff: an all-white 16-gray
// clear image is a run of 0xFF bytes, which is valid UTF-8.
func TestClearImageDeclaresOctetStreamForGrey16(t *testing.T) {
	rec := postJSON(t, "/clearImage", ImageProperties{Width: 64, Height: 32, ColorSpace: grey16Palette()})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type %q, want application/octet-stream", got)
	}
}

func TestClearImageHandlerReturnsUniformColor(t *testing.T) {
	const w, h = 40, 24
	rec := postJSON(t, "/clearImage", ImageProperties{Width: w, Height: h, ColorSpace: epd7in3fPalette})

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	body := rec.Body.Bytes()
	if want := w * h / 2; len(body) != want {
		t.Fatalf("got %d bytes, want %d", len(body), want)
	}
	for i, b := range body {
		if b != body[0] {
			t.Fatalf("clear image is not uniform: byte %d = %#x, byte 0 = %#x", i, b, body[0])
		}
	}
	if body[0]>>4 != body[0]&0xF {
		t.Errorf("the two pixels in a byte differ: %#x", body[0])
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(body)) {
		t.Errorf("Content-Length %q, want %d", got, len(body))
	}
}

func TestClearImageHandlerColorIsWhite(t *testing.T) {
	rec := postJSON(t, "/clearImage", ImageProperties{Width: 8, Height: 4, ColorSpace: epd7in3fPalette})
	white := epd7in3fPalette[1].Code
	if want := white<<4 | white; rec.Body.Bytes()[0] != want {
		t.Errorf("got %#x, want %#x", rec.Body.Bytes()[0], want)
	}
}

func TestHandlersRejectMalformedJSON(t *testing.T) {
	endpoints := []string{"/fetchImage", "/calibrationImage", "/clearImage"}
	bodies := map[string]string{
		"garbage":            "not json",
		"truncated object":   `{"width": 800,`,
		"array not object":   `[1,2,3]`,
		"wrong field type":   `{"width": "800"}`,
		"empty body":         ``,
		"bad color_space":    `{"width":8,"height":4,"color_space":"nope"}`,
		"bad rgb_color type": `{"width":8,"height":4,"color_space":[{"color_code":0,"rgb_color":"black"}]}`,
	}
	for _, ep := range endpoints {
		for name, body := range bodies {
			t.Run(ep+"/"+name, func(t *testing.T) {
				rec := postRaw(t, ep, body)
				if rec.Code != http.StatusBadRequest {
					t.Errorf("status %d, want 400 (body %q)", rec.Code, rec.Body.String())
				}
			})
		}
	}
}

func TestHandlersRejectNonPost(t *testing.T) {
	router := newTestRouter()
	for _, ep := range []string{"/fetchImage", "/calibrationImage", "/clearImage"} {
		req := httptest.NewRequest(http.MethodGet, ep, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: status %d, want 405", ep, rec.Code)
		}
	}
}

// An empty (or missing) color_space used to divide by zero in
// GenerateCalibrationImage and index out of range in GenerateClearImage, leaving
// chi's Recoverer to turn an untrusted body into a 500. It is a bad request.
func TestHandlersRejectEmptyColorSpace(t *testing.T) {
	for _, ep := range []string{"/fetchImage", "/calibrationImage", "/clearImage"} {
		t.Run(ep, func(t *testing.T) {
			rec := postJSON(t, ep, ImageProperties{Width: 8, Height: 4, ColorSpace: ColorSpace{}})
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400 (body %q)", rec.Code, rec.Body.String())
			}
		})
	}
}

// Degenerate and absurd dimensions are rejected before they reach the pipeline,
// where they used to divide by zero or allocate unboundedly.
func TestHandlersRejectOutOfRangeDimensions(t *testing.T) {
	cases := map[string]ImageProperties{
		"zero size":       {Width: 0, Height: 0, ColorSpace: epd7in3fPalette},
		"zero width":      {Width: 0, Height: 4, ColorSpace: epd7in3fPalette},
		"zero height":     {Width: 8, Height: 0, ColorSpace: epd7in3fPalette},
		"negative width":  {Width: -8, Height: 4, ColorSpace: epd7in3fPalette},
		"negative height": {Width: 8, Height: -4, ColorSpace: epd7in3fPalette},
		"absurd width":    {Width: maxDimension + 1, Height: 4, ColorSpace: epd7in3fPalette},
		"absurd height":   {Width: 8, Height: maxDimension + 1, ColorSpace: epd7in3fPalette},
	}
	for _, ep := range []string{"/fetchImage", "/calibrationImage", "/clearImage"} {
		for name, props := range cases {
			t.Run(ep+"/"+name, func(t *testing.T) {
				rec := postJSON(t, ep, props)
				if rec.Code != http.StatusBadRequest {
					t.Errorf("status %d, want 400 (body %q)", rec.Code, rec.Body.String())
				}
			})
		}
	}
}

// The largest shipped panel must stay inside the accepted range.
func TestHandlersAcceptTheLargestShippedGeometry(t *testing.T) {
	rec := postJSON(t, "/clearImage", ImageProperties{Width: 1872, Height: 1404, ColorSpace: grey16Palette()})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if want := 1872 * 1404 / 2; rec.Body.Len() != want {
		t.Errorf("got %d bytes, want %d", rec.Body.Len(), want)
	}
}

// paletteOfSize builds a valid color space of n entries. Codes repeat every 16
// because that is all a nibble holds, which is exactly why the entry count and
// the code range are bounded separately.
func paletteOfSize(n int) ColorSpace {
	cs := make(ColorSpace, n)
	for i := range cs {
		v := float64(i%16) / 15.0
		cs[i].Color = Colorf{v, v, v}
		cs[i].Code = uint8(i % 16)
	}
	return cs
}

// The endpoints are unauthenticated, so nothing but a hard cap stops an
// arbitrary body: 200 000 color_space entries were accepted, which extrapolated
// to ~52 minutes of CPU for a single 1872x1404 fetch.
//
// The "after the object" case is the one json.Decoder does not catch by
// itself: it stops at the end of the first value, so the padding is never read
// and the cap is never reached unless the handler insists the object is the
// whole body.
func TestHandlersRejectOversizeBody(t *testing.T) {
	const valid = `{"width":8,"height":4,"color_space":[{"color_code":0,"rgb_color":[0,0,0]}]`
	padding := strings.Repeat("x", maxBodyBytes)
	bodies := map[string]string{
		"padding inside the object": valid + `,"padding":"` + padding + `"}`,
		"padding after the object":  valid + `}"` + padding + `"`,
	}
	for _, ep := range []string{"/fetchImage", "/calibrationImage", "/clearImage"} {
		for name, body := range bodies {
			t.Run(ep+"/"+name, func(t *testing.T) {
				rec := postRaw(t, ep, body)
				if rec.Code != http.StatusRequestEntityTooLarge {
					t.Errorf("status %d, want 413 (body %q)", rec.Code, rec.Body.String())
				}
			})
		}
	}
}

// A second JSON value after the first is not a frame talking; it is the shape
// a body-cap bypass takes, so it is a bad request even when it is small.
func TestHandlersRejectTrailingContent(t *testing.T) {
	const valid = `{"width":8,"height":4,"color_space":[{"color_code":0,"rgb_color":[0,0,0]}]}`
	bodies := map[string]string{
		"second object": valid + valid,
		"junk":          valid + "garbage",
		"array":         valid + "[1,2,3]",
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			rec := postRaw(t, "/clearImage", body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400 (body %q)", rec.Code, rec.Body.String())
			}
		})
	}
	// Trailing whitespace is not trailing content.
	if rec := postRaw(t, "/clearImage", valid+"\n"); rec.Code != http.StatusOK {
		t.Errorf("a trailing newline was rejected: status %d, body %q", rec.Code, rec.Body.String())
	}
}

// Each side can be inside the per-side cap while the product is absurd:
// 10000x10000 answered 200 with a 50 MB body and ~350 MiB of heap, enough to
// OOM the 256 MiB service VM.
func TestHandlersRejectTooManyPixels(t *testing.T) {
	cases := map[string]ImageProperties{
		"square":     {Width: maxDimension, Height: maxDimension, ColorSpace: epd7in3fPalette},
		"just over":  {Width: 4000, Height: maxPixels/4000 + 1, ColorSpace: epd7in3fPalette},
		"long strip": {Width: maxDimension, Height: maxPixels/maxDimension + 1, ColorSpace: epd7in3fPalette},
	}
	for _, ep := range []string{"/fetchImage", "/calibrationImage", "/clearImage"} {
		for name, props := range cases {
			t.Run(ep+"/"+name, func(t *testing.T) {
				rec := postJSON(t, ep, props)
				if rec.Code != http.StatusBadRequest {
					t.Errorf("status %d, want 400 (body %q)", rec.Code, rec.Body.String())
				}
			})
		}
	}
}

// The cap itself is still served — it is a bound, not a panel list.
func TestClearImageAcceptsTheMaximumPixelCount(t *testing.T) {
	const w, h = 4000, maxPixels / 4000
	rec := postJSON(t, "/clearImage", ImageProperties{Width: w, Height: h, ColorSpace: epd7in3fPalette})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if want := w * h / 2; rec.Body.Len() != want {
		t.Errorf("got %d bytes, want %d", rec.Body.Len(), want)
	}
}

// The nearest-color search is linear in the palette, so an unbounded
// color_space is a CPU amplifier even when the body itself is small.
func TestHandlersRejectOversizeColorSpace(t *testing.T) {
	props := ImageProperties{Width: 8, Height: 4, ColorSpace: paletteOfSize(maxColorSpaceEntries + 1)}
	for _, ep := range []string{"/fetchImage", "/calibrationImage", "/clearImage"} {
		t.Run(ep, func(t *testing.T) {
			rec := postJSON(t, ep, props)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400 (body %q)", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandlersAcceptTheLargestAllowedColorSpace(t *testing.T) {
	rec := postJSON(t, "/calibrationImage", ImageProperties{Width: 8, Height: 4, ColorSpace: paletteOfSize(maxColorSpaceEntries)})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
}

// A pixel is a nibble. A wider code used to be packed anyway: 0xF0 wrote
// nothing of its own and clobbered the neighbouring pixel instead, so a bad
// palette produced a plausible-but-wrong image rather than an error.
func TestHandlersRejectOutOfRangeColorCode(t *testing.T) {
	cases := map[string]uint8{
		"one past the nibble": maxColorCode + 1,
		"whole byte":          0xFF,
		"high nibble only":    0xF0,
	}
	for _, ep := range []string{"/fetchImage", "/calibrationImage", "/clearImage"} {
		for name, code := range cases {
			t.Run(ep+"/"+name, func(t *testing.T) {
				cs := ColorSpace{{Colorf{0, 0, 0}, 0}, {Colorf{1, 1, 1}, code}}
				rec := postJSON(t, ep, ImageProperties{Width: 8, Height: 4, ColorSpace: cs})
				if rec.Code != http.StatusBadRequest {
					t.Errorf("status %d, want 400 (body %q)", rec.Code, rec.Body.String())
				}
			})
		}
	}
}

// uint8(channel*255) is implementation-defined outside [0, 1] — measured -5 ->
// 5, 300 -> 212, 1e9 -> 0 — so an out-of-range channel silently matches pixels
// against a color nobody asked for.
func TestHandlersRejectOutOfRangeRGBColor(t *testing.T) {
	cases := map[string]Colorf{
		"negative":       {-5, 0, 0},
		"slightly under": {0, -0.0001, 0},
		"just over one":  {0, 0, 1.0001},
		"far over":       {300, 0, 0},
		"absurd":         {0, 1e9, 0},
	}
	for _, ep := range []string{"/fetchImage", "/calibrationImage", "/clearImage"} {
		for name, c := range cases {
			t.Run(ep+"/"+name, func(t *testing.T) {
				cs := ColorSpace{{Colorf{0, 0, 0}, 0}, {c, 1}}
				rec := postJSON(t, ep, ImageProperties{Width: 8, Height: 4, ColorSpace: cs})
				if rec.Code != http.StatusBadRequest {
					t.Errorf("status %d, want 400 (body %q)", rec.Code, rec.Body.String())
				}
			})
		}
	}
}

// The bounds must not cost a real frame its picture: this is the byte-for-byte
// body the EPD7IN3F driver sends.
func TestHandlersAcceptTheFirmwareRequestBody(t *testing.T) {
	for _, ep := range []string{"/calibrationImage", "/clearImage"} {
		t.Run(ep, func(t *testing.T) {
			rec := postRaw(t, ep, firmwareRequestBody)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
			}
			if want := 800 * 480 / 2; rec.Body.Len() != want {
				t.Errorf("got %d bytes, want %d", rec.Body.Len(), want)
			}
		})
	}
}

// maxBodyBytes is justified against the biggest body any shipped panel sends —
// the 16-entry grey16 palette — so that is the one that has to be measured,
// and with room to spare rather than merely fitting.
func TestTheLargestFirmwareBodyFitsTheByteCap(t *testing.T) {
	props := ImageProperties{Width: 1872, Height: 1404, ColorSpace: grey16Palette()}
	if got := mustJSON(t, props).Len(); got > maxBodyBytes/2 {
		t.Errorf("the grey16 request body is %d bytes, over half the %d byte cap", got, maxBodyBytes)
	}
}

// A conversion costs hundreds of MB, so the number in flight has to be bounded
// or N frames waking on the same minute multiply the peak by N. With every
// slot taken, a caller that has already given up is answered rather than
// queued behind work it will never read.
func TestImageRequestsAreConcurrencyLimited(t *testing.T) {
	for i := 0; i < maxConcurrentRequests; i++ {
		imageSlots <- struct{}{}
	}
	t.Cleanup(func() {
		for i := 0; i < maxConcurrentRequests; i++ {
			<-imageSlots
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/clearImage",
		mustJSON(t, ImageProperties{Width: 8, Height: 4, ColorSpace: epd7in3fPalette})).WithContext(ctx)
	rec := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503 (body %q)", rec.Code, rec.Body.String())
	}
}

// The body must be decoded before the request queues for a conversion slot,
// not after: the server's read deadline is armed when the request line
// arrives, so a frame that waited in the queue longer than readTimeout would
// otherwise have its body read fail and be answered 400 — a stale picture for
// a whole sleep cycle, on precisely the busy morning the queue exists for.
// With every slot held, a malformed body has to come back 400 straight away;
// if it queued first, this request's context would expire in the queue and it
// would be answered 503 instead.
func TestBodyIsDecodedBeforeQueueing(t *testing.T) {
	for i := 0; i < maxConcurrentRequests; i++ {
		imageSlots <- struct{}{}
	}
	t.Cleanup(func() {
		for i := 0; i < maxConcurrentRequests; i++ {
			<-imageSlots
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/clearImage", strings.NewReader(`{"width": 0`)).WithContext(ctx)
	rec := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400 before any slot is waited for (body %q)", rec.Code, rec.Body.String())
	}
}

// Every exit from a handler has to give the slot back — a rejected request
// most of all, since those are the cheap ones to send in bulk. A leak here
// wedges the server after maxConcurrentRequests bad requests, so this test
// hangs rather than fails if the release is dropped.
func TestConcurrencySlotsAreReleasedByRejectedRequests(t *testing.T) {
	for i := 0; i < maxConcurrentRequests+2; i++ {
		rec := postJSON(t, "/clearImage", ImageProperties{Width: 0, Height: 0, ColorSpace: epd7in3fPalette})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("request %d: status %d, want 400", i, rec.Code)
		}
	}
	if rec := postJSON(t, "/clearImage", ImageProperties{Width: 8, Height: 4, ColorSpace: epd7in3fPalette}); rec.Code != http.StatusOK {
		t.Fatalf("status %d after the rejected requests, want 200", rec.Code)
	}
	if len(imageSlots) != 0 {
		t.Errorf("%d slots are still held", len(imageSlots))
	}
}

func TestCalibrationImageAllShippedPalettes(t *testing.T) {
	cases := []struct {
		name    string
		w, h    int
		palette ColorSpace
	}{
		{"epd7in3f 800x480", 800, 480, epd7in3fPalette},
		{"el133uf1 1200x1600", 1200, 1600, el133uf1Palette},
		{"it8951 1872x1404", 1872, 1404, grey16Palette()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postJSON(t, "/calibrationImage", ImageProperties{Width: tc.w, Height: tc.h, ColorSpace: tc.palette})
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d", rec.Code)
			}
			if want := tc.w * tc.h / 2; rec.Body.Len() != want {
				t.Errorf("got %d bytes, want %d", rec.Body.Len(), want)
			}
		})
	}
}

// ===========================================================================
// fetchImage
// ===========================================================================

func TestFetchImageEndToEnd(t *testing.T) {
	imgDir := t.TempDir()
	writeTempPNG(t, imgDir, "only.png", gradientRGBA(64, 48))
	withImageDir(t, imgDir)

	const w, h = 16, 8
	rec := postJSON(t, "/fetchImage", ImageProperties{Width: w, Height: h, ColorSpace: epd7in3fPalette})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if want := w * h / 2; rec.Body.Len() != want {
		t.Errorf("got %d bytes, want %d (width*height/2)", rec.Body.Len(), want)
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(rec.Body.Len()) {
		t.Errorf("Content-Length %q, want %d", got, rec.Body.Len())
	}
	// Every nibble must be a color code the client actually declared.
	valid := map[byte]bool{}
	for _, def := range epd7in3fPalette {
		valid[def.Code] = true
	}
	for i, b := range rec.Body.Bytes() {
		if !valid[b>>4] || !valid[b&0xF] {
			t.Fatalf("byte %d = %#x contains a code outside the requested color space", i, b)
		}
	}
}

// The real image directory is mostly JPEGs, which decode to *image.YCbCr, not
// the *image.RGBA every other handler test feeds the pipeline. The whole
// pipeline (temp-PNG encode, crop, resize, quantize) must cope with it.
func TestFetchImageEndToEndJPEG(t *testing.T) {
	imgDir := t.TempDir()
	writeTempJPEG(t, imgDir, "only.jpg", gradientRGBA(64, 48))
	withImageDir(t, imgDir)

	const w, h = 16, 8
	rec := postJSON(t, "/fetchImage", ImageProperties{Width: w, Height: h, ColorSpace: epd7in3fPalette})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if want := w * h / 2; rec.Body.Len() != want {
		t.Errorf("got %d bytes, want %d (width*height/2)", rec.Body.Len(), want)
	}
}

// A GIF's first frame may be anchored away from the origin (its frame offset in
// the logical screen), so this is the one decodable input whose bounds don't
// start at (0,0). The crop rectangle must land inside those bounds; getting the
// coordinate spaces wrong yields an empty intersection and a 0-byte 200 reply.
func TestFetchImageEndToEndOffsetFrameGIF(t *testing.T) {

	// The frame offset exceeds the frame size, so an untranslated crop rect
	// intersects the frame's bounds to an empty rectangle rather than merely a
	// shifted region — the failure mode is a 0-byte reply, not subtly wrong bytes.
	frame := image.NewPaletted(image.Rect(100, 50, 164, 98), palette.Plan9) // 64x48 at (100,50)
	for y := 50; y < 98; y++ {
		for x := 100; x < 164; x++ {
			frame.Set(x, y, color.RGBA{uint8(x * 3), uint8(y * 4), 128, 255})
		}
	}
	imgDir := t.TempDir()
	f, err := os.Create(filepath.Join(imgDir, "only.gif"))
	if err != nil {
		t.Fatalf("create gif: %v", err)
	}
	if err := gif.EncodeAll(f, &gif.GIF{
		Image:  []*image.Paletted{frame},
		Delay:  []int{0},
		Config: image.Config{ColorModel: color.Palette(palette.Plan9), Width: 164, Height: 98},
	}); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	f.Close()

	// Confirm the fixture really decodes with a non-zero origin; otherwise the
	// test silently stops covering the offset path.
	decoded, err := ReadImage(filepath.Join(imgDir, "only.gif"))
	if err != nil {
		t.Fatalf("ReadImage: %v", err)
	}
	if decoded.Bounds().Min == (image.Point{}) {
		t.Fatal("fixture decoded origin-anchored; it no longer exercises the offset-frame path")
	}

	withImageDir(t, imgDir)

	const w, h = 16, 8
	rec := postJSON(t, "/fetchImage", ImageProperties{Width: w, Height: h, ColorSpace: epd7in3fPalette})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if want := w * h / 2; rec.Body.Len() != want {
		t.Errorf("got %d bytes, want %d (width*height/2)", rec.Body.Len(), want)
	}
}

func TestFetchImageIsDeterministicForAGivenImage(t *testing.T) {
	imgDir := t.TempDir()
	writeTempPNG(t, imgDir, "only.png", gradientRGBA(64, 48))
	withImageDir(t, imgDir)

	props := ImageProperties{Width: 16, Height: 8, ColorSpace: epd7in3fPalette}
	first := postJSON(t, "/fetchImage", props).Body.String()
	for i := 0; i < 3; i++ {
		if got := postJSON(t, "/fetchImage", props).Body.String(); got != first {
			t.Fatalf("fetchImage is not deterministic for a single-image directory (run %d)", i)
		}
	}
}

func TestFetchImageHonoursFlips(t *testing.T) {
	imgDir := t.TempDir()
	// Top half white, bottom half black: a vertical flip must swap them.
	src := solidRGBA(64, 48, color.RGBA{255, 255, 255, 255})
	for y := 24; y < 48; y++ {
		for x := 0; x < 64; x++ {
			src.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
		}
	}
	writeTempPNG(t, imgDir, "only.png", src)
	withImageDir(t, imgDir)

	props := ImageProperties{Width: 16, Height: 8, ColorSpace: bwPalette}
	normal := postJSON(t, "/fetchImage", props).Body.Bytes()

	props.FlipVertical = true
	flipped := postJSON(t, "/fetchImage", props).Body.Bytes()

	if len(normal) != len(flipped) || len(normal) == 0 {
		t.Fatalf("unexpected lengths %d / %d", len(normal), len(flipped))
	}
	if string(normal) == string(flipped) {
		t.Fatal("a vertical flip produced identical output for a vertically asymmetric image")
	}
	// First byte of the un-flipped image is white (code 1), of the flipped one black.
	if normal[0] != 0x11 {
		t.Errorf("un-flipped top-left should be white, got %#x", normal[0])
	}
	if flipped[0] != 0x00 {
		t.Errorf("flipped top-left should be black, got %#x", flipped[0])
	}
}

func TestFetchImageHonoursHorizontalFlip(t *testing.T) {
	imgDir := t.TempDir()
	src := solidRGBA(64, 48, color.RGBA{255, 255, 255, 255})
	for y := 0; y < 48; y++ {
		for x := 32; x < 64; x++ {
			src.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
		}
	}
	writeTempPNG(t, imgDir, "only.png", src)
	withImageDir(t, imgDir)

	props := ImageProperties{Width: 16, Height: 8, ColorSpace: bwPalette}
	normal := postJSON(t, "/fetchImage", props).Body.Bytes()
	props.FlipHorizonal = true
	flipped := postJSON(t, "/fetchImage", props).Body.Bytes()

	if normal[0] != 0x11 {
		t.Errorf("un-flipped left edge should be white, got %#x", normal[0])
	}
	if flipped[0] != 0x00 {
		t.Errorf("horizontally flipped left edge should be black, got %#x", flipped[0])
	}
}

// A picture with no face in it must not cost the frame its picture: the crop
// falls back to a centered one and the response is a normal, full-length image.
func TestFetchImageWithoutFacesStillServesAnImage(t *testing.T) {
	imgDir := t.TempDir()
	writeTempPNG(t, imgDir, "only.png", gradientRGBA(64, 48))
	withImageDir(t, imgDir)

	const w, h = 16, 8
	rec := postJSON(t, "/fetchImage", ImageProperties{Width: w, Height: h, ColorSpace: epd7in3fPalette})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if want := w * h / 2; rec.Body.Len() != want {
		t.Fatalf("got %d bytes, want %d (width*height/2)", rec.Body.Len(), want)
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(rec.Body.Len()) {
		t.Errorf("Content-Length %q, want %d", got, rec.Body.Len())
	}
}

// An undecodable file in the image directory fails the one request with a 5xx.
// It used to return a nil image.Image that flipImage dereferenced, so the
// request panicked and only chi's Recoverer kept the server up. The fixture
// carries an image extension so that it is drawn the way a real photo would be,
// rather than only as the last resort a .txt would be.
func TestFetchImageNonImageFile(t *testing.T) {
	imgDir := t.TempDir()
	if err := writeFile(filepath.Join(imgDir, "readme.png"), "not an image"); err != nil {
		t.Fatal(err)
	}
	withImageDir(t, imgDir)

	rec := postJSON(t, "/fetchImage", ImageProperties{Width: 16, Height: 8, ColorSpace: epd7in3fPalette})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500", rec.Code)
	}
}

// An empty image directory is a server-side problem, not a bad request.
func TestFetchImageEmptyImageDirectory(t *testing.T) {
	withImageDir(t, t.TempDir())

	rec := postJSON(t, "/fetchImage", ImageProperties{Width: 16, Height: 8, ColorSpace: epd7in3fPalette})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500", rec.Code)
	}
}

// One unusable file among usable ones used to cost the frame a whole sleep
// cycle: the draw is uniform, so with k bad files out of N every wake had a k/N
// chance of a 500 and hours of stale picture. The request must walk on instead.
//
// The single good file is deliberately outnumbered: a bounded number of retries
// would fail here, and pushing the last-served file to the back of the queue
// must not push the only working file out of reach on the second request.
func TestFetchImageRetriesPastUndecodableFiles(t *testing.T) {
	imgDir := t.TempDir()
	// Decodable extensions, undecodable content: exactly what a truncated or
	// misnamed file looks like, and the only case the extension filter cannot
	// catch on its own.
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("broken%d.png", i)
		if err := writeFile(filepath.Join(imgDir, name), "not an image"); err != nil {
			t.Fatal(err)
		}
	}
	good := writeTempPNG(t, imgDir, "good.png", gradientRGBA(64, 48))
	withImageDir(t, imgDir)

	const w, h = 16, 8
	for i := 0; i < 10; i++ {
		rec := postJSON(t, "/fetchImage", ImageProperties{Width: w, Height: h, ColorSpace: epd7in3fPalette})
		if rec.Code != http.StatusOK {
			t.Fatalf("iteration %d: status %d, body %q (the one good file is %q)", i, rec.Code, rec.Body.String(), good)
		}
		if want := w * h / 2; rec.Body.Len() != want {
			t.Fatalf("iteration %d: got %d bytes, want %d", i, rec.Body.Len(), want)
		}
	}
}

// The walk must terminate: when nothing in the directory decodes, the request
// is still a 500 (and the server stays up).
func TestFetchImageAllCandidatesUndecodable(t *testing.T) {
	imgDir := t.TempDir()
	for _, name := range []string{"a.png", "b.jpg", "c.jpeg", "d.gif"} {
		if err := writeFile(filepath.Join(imgDir, name), "not an image"); err != nil {
			t.Fatal(err)
		}
	}
	withImageDir(t, imgDir)

	rec := postJSON(t, "/fetchImage", ImageProperties{Width: 16, Height: 8, ColorSpace: epd7in3fPalette})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 (body %q)", rec.Code, rec.Body.String())
	}
	// The handler's own error, not a panic caught by chi's Recoverer.
	if !strings.Contains(rec.Body.String(), "no image available") {
		t.Errorf("body %q, want the handler's own 'no image available'", rec.Body.String())
	}
}

// A .DS_Store or a .heic next to the photos is not a reason to show nothing:
// dotfiles never enter the draw, and an undecodable extension is only ever
// tried after the real pictures.
func TestFetchImageIgnoresUndecodableExtensions(t *testing.T) {
	imgDir := t.TempDir()
	writeTempPNG(t, imgDir, "only.png", gradientRGBA(64, 48))
	for _, name := range []string{".DS_Store", "clip.mp4", "phone.heic", "readme.txt", ".only.png.tmp7"} {
		if err := writeFile(filepath.Join(imgDir, name), "not an image"); err != nil {
			t.Fatal(err)
		}
	}
	withImageDir(t, imgDir)

	const w, h = 16, 8
	for i := 0; i < 20; i++ {
		rec := postJSON(t, "/fetchImage", ImageProperties{Width: w, Height: h, ColorSpace: epd7in3fPalette})
		if rec.Code != http.StatusOK {
			t.Fatalf("iteration %d: status %d, body %q", i, rec.Code, rec.Body.String())
		}
		if want := w * h / 2; rec.Body.Len() != want {
			t.Fatalf("iteration %d: got %d bytes, want %d", i, rec.Body.Len(), want)
		}
	}
}

// A uniform draw repeats back-to-back 1/N of the time, which on a frame that
// refreshes a handful of times a day is very visible. Consecutive requests must
// not serve the same file.
func TestFetchImageDoesNotRepeatTheSameImageBackToBack(t *testing.T) {
	imgDir := t.TempDir()
	writeTempPNG(t, imgDir, "white.png", solidRGBA(64, 48, color.RGBA{255, 255, 255, 255}))
	writeTempPNG(t, imgDir, "black.png", solidRGBA(64, 48, color.RGBA{0, 0, 0, 255}))
	withImageDir(t, imgDir)

	props := ImageProperties{Width: 16, Height: 8, ColorSpace: bwPalette}
	seen := map[string]bool{}
	var prev string
	for i := 0; i < 12; i++ {
		rec := postJSON(t, "/fetchImage", props)
		if rec.Code != http.StatusOK {
			t.Fatalf("iteration %d: status %d, body %q", i, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if i > 0 && body == prev {
			t.Fatalf("iteration %d served the same image twice in a row", i)
		}
		seen[body] = true
		prev = body
	}
	if len(seen) != 2 {
		t.Errorf("expected both images to be served, saw %d distinct results", len(seen))
	}
}

// Sub-directories in the image directory must not break the draw: fetchImage
// used to receive "" and hand it to a ReadImage that called os.Exit(1).
func TestFetchImageIgnoresSubdirectories(t *testing.T) {
	imgDir := t.TempDir()
	writeTempPNG(t, imgDir, "only.png", gradientRGBA(64, 48))
	for _, sub := range []string{"sub1", "sub2", "sub3"} {
		if err := os.Mkdir(filepath.Join(imgDir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	withImageDir(t, imgDir)

	const w, h = 16, 8
	for i := 0; i < 20; i++ {
		rec := postJSON(t, "/fetchImage", ImageProperties{Width: w, Height: h, ColorSpace: epd7in3fPalette})
		if rec.Code != http.StatusOK {
			t.Fatalf("iteration %d: status %d, body %q", i, rec.Code, rec.Body.String())
		}
		if want := w * h / 2; rec.Body.Len() != want {
			t.Fatalf("iteration %d: got %d bytes, want %d", i, rec.Body.Len(), want)
		}
	}
}

// ===========================================================================
// imageCandidates
// ===========================================================================

// drawOne returns the file the next fetch would try first.
func drawOne(t *testing.T, dir, last string) string {
	t.Helper()
	candidates, err := imageCandidates(dir, last)
	if err != nil {
		t.Fatalf("imageCandidates(%q): %v", dir, err)
	}
	return candidates[0]
}

// mustCandidates returns the candidate list as a set.
func mustCandidates(t *testing.T, dir, last string) map[string]bool {
	t.Helper()
	candidates, err := imageCandidates(dir, last)
	if err != nil {
		t.Fatalf("imageCandidates(%q): %v", dir, err)
	}
	set := map[string]bool{}
	for _, c := range candidates {
		set[c] = true
	}
	if len(set) != len(candidates) {
		t.Fatalf("candidate list has duplicates: %v", candidates)
	}
	return set
}

func TestImageCandidatesDrawsFromTheDirectory(t *testing.T) {
	dir := t.TempDir()
	want := map[string]bool{}
	for _, n := range []string{"a.png", "b.png", "c.png", "d.png"} {
		p := filepath.Join(dir, n)
		if err := writeFile(p, "x"); err != nil {
			t.Fatal(err)
		}
		want[p] = true
	}
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		got := drawOne(t, dir, "")
		if !want[got] {
			t.Fatalf("returned %q which is not one of the files in the directory", got)
		}
		seen[got] = true
	}
	if len(seen) < 2 {
		t.Errorf("expected the selection to vary across 200 draws, only saw %v", seen)
	}
}

// The whole directory is offered, not just the first pick: that is what lets
// the decode walk reach a working file past any number of broken ones.
func TestImageCandidatesOffersEveryFile(t *testing.T) {
	dir := t.TempDir()
	want := map[string]bool{}
	for _, n := range []string{"a.png", "b.png", "c.png", "d.png", "e.png"} {
		p := filepath.Join(dir, n)
		if err := writeFile(p, "x"); err != nil {
			t.Fatal(err)
		}
		want[p] = true
	}
	got := mustCandidates(t, dir, "")
	if len(got) != len(want) {
		t.Fatalf("got %d candidates, want %d: %v", len(got), len(want), got)
	}
	for p := range want {
		if !got[p] {
			t.Errorf("%q is missing from the candidate list", p)
		}
	}
}

func TestImageCandidatesMissingDirectory(t *testing.T) {
	if _, err := imageCandidates(filepath.Join(t.TempDir(), "does-not-exist"), ""); err == nil {
		t.Error("expected an error for a missing directory")
	}
}

// An empty candidate list would index out of range downstream, so it has to be
// reported as an error up front.
func TestImageCandidatesEmptyDirectory(t *testing.T) {
	got, err := imageCandidates(t.TempDir(), "")
	if err == nil {
		t.Fatalf("expected an error for an empty directory, got %q", got)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error should wrap os.ErrNotExist, got %v", err)
	}
}

// A directory containing only sub-directories has no image to serve.
func TestImageCandidatesOnlySubdirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := imageCandidates(dir, ""); err == nil {
		t.Errorf("expected an error when the directory holds no files, got %q", got)
	}
}

// A directory of nothing but dotfiles is empty as far as the draw is concerned;
// the fallback must not resurrect them.
func TestImageCandidatesOnlyDotfiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".DS_Store", ".hidden.png"} {
		if err := writeFile(filepath.Join(dir, name), "x"); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := imageCandidates(dir, ""); err == nil {
		t.Errorf("expected an error when the directory holds only dotfiles, got %q", got)
	}
}

// The draw is over the files only. Drawing over every dirent but stepping only
// past files made the loop fall through and return os.ErrNotExist whenever the
// image directory held sub-directories.
func TestImageCandidatesWithSubdirectories(t *testing.T) {
	dir := t.TempDir()
	only := filepath.Join(dir, "only.png")
	if err := writeFile(only, "x"); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"sub1", "sub2", "sub3"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 100; i++ {
		if got := drawOne(t, dir, ""); got != only {
			t.Fatalf("iteration %d: got %q, want %q", i, got, only)
		}
	}
}

// A .heic or a .mp4 next to the photos is an all-but-certain decode failure, so
// it is never tried while a real picture is still on the queue.
func TestImageCandidatesTryDecodableExtensionsFirst(t *testing.T) {
	dir := t.TempDir()
	only := filepath.Join(dir, "only.png")
	if err := writeFile(only, "x"); err != nil {
		t.Fatal(err)
	}
	junk := []string{"notes.txt", "clip.mp4", "phone.heic", "archive.zip", "noext"}
	for _, name := range junk {
		if err := writeFile(filepath.Join(dir, name), "x"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 100; i++ {
		if got := drawOne(t, dir, ""); got != only {
			t.Fatalf("iteration %d drew %q before the one file with a decodable extension %q", i, got, only)
		}
	}
	// They stay on the queue behind it: a directory is not allowed to become
	// unservable because its only decodable-looking file is corrupt.
	if got := mustCandidates(t, dir, ""); len(got) != len(junk)+1 {
		t.Errorf("got %d candidates, want %d: %v", len(got), len(junk)+1, got)
	}
}

// Dotfiles are either editor/OS metadata (.DS_Store) or rsync's partial
// transfers (.photo.jpg.XXXXXX), which decode only some of the time.
func TestImageCandidatesSkipsDotfiles(t *testing.T) {
	dir := t.TempDir()
	only := filepath.Join(dir, "only.jpg")
	if err := writeFile(only, "x"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".DS_Store", ".hidden.png", ".only.jpg.tmp7Kq2", ".gif"} {
		if err := writeFile(filepath.Join(dir, name), "x"); err != nil {
			t.Fatal(err)
		}
	}
	got := mustCandidates(t, dir, "")
	if len(got) != 1 || !got[only] {
		t.Errorf("candidates %v, want only %q", got, only)
	}
}

// Every extension the registered decoders handle must stay drawable, in any
// case: a directory of .JPG files off a camera is not an empty directory.
func TestImageCandidatesAcceptEveryDecodableExtension(t *testing.T) {
	dir := t.TempDir()
	want := map[string]bool{}
	for _, name := range []string{"a.png", "b.jpg", "c.jpeg", "d.gif", "e.PNG", "f.JPG", "g.JPEG", "h.GIF", "i.jfif"} {
		p := filepath.Join(dir, name)
		if err := writeFile(p, "x"); err != nil {
			t.Fatal(err)
		}
		want[p] = true
	}
	got := mustCandidates(t, dir, "")
	for p := range want {
		if !got[p] {
			t.Errorf("%q was filtered out even though its extension is decodable", p)
		}
	}
}

// The extension filter must not turn a directory of extension-less exports into
// "no image available": image.Decode goes by content, so every non-dotfile is
// worth sniffing once the likelier candidates are exhausted.
func TestImageCandidatesOfferFilesWithoutADecodableExtension(t *testing.T) {
	dir := t.TempDir()
	want := map[string]bool{}
	for _, name := range []string{"export", "readme.txt", "movie.mp4"} {
		p := filepath.Join(dir, name)
		if err := writeFile(p, "x"); err != nil {
			t.Fatal(err)
		}
		want[p] = true
	}
	if err := writeFile(filepath.Join(dir, ".DS_Store"), "x"); err != nil {
		t.Fatal(err)
	}
	got := mustCandidates(t, dir, "")
	if len(got) != len(want) {
		t.Fatalf("got %d candidates, want %d: %v", len(got), len(want), got)
	}
	for p := range want {
		if !got[p] {
			t.Errorf("%q is missing from the candidate list", p)
		}
	}
}

// One corrupt file with an image extension must not hide a directory full of
// extension-less pictures behind it: preferring an extension is a hint about
// what to try first, never a reason to leave a real picture unreachable.
func TestFetchImageFallsPastACorruptPreferredFile(t *testing.T) {
	imgDir := t.TempDir()
	if err := writeFile(filepath.Join(imgDir, "cover.jpg"), ""); err != nil {
		t.Fatal(err)
	}
	// Real PNGs, written without an extension (a phone/export dump).
	for i := 0; i < 4; i++ {
		writeTempPNG(t, imgDir, fmt.Sprintf("export%d", i), gradientRGBA(64, 48))
	}
	withImageDir(t, imgDir)

	const w, h = 16, 8
	for i := 0; i < 10; i++ {
		rec := postJSON(t, "/fetchImage", ImageProperties{Width: w, Height: h, ColorSpace: epd7in3fPalette})
		if rec.Code != http.StatusOK {
			t.Fatalf("iteration %d: status %d, body %q", i, rec.Code, rec.Body.String())
		}
		if want := w * h / 2; rec.Body.Len() != want {
			t.Fatalf("iteration %d: got %d bytes, want %d", i, rec.Body.Len(), want)
		}
	}
}

// The last-served file goes to the back of the queue, so the next request shows
// a different photo — but it stays in the queue, so it is still there to fall
// back on when everything else is broken.
func TestImageCandidatesPutTheLastServedFileLast(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for _, n := range []string{"a.png", "b.png", "c.png"} {
		p := filepath.Join(dir, n)
		if err := writeFile(p, "x"); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	for i := 0; i < 100; i++ {
		candidates, err := imageCandidates(dir, paths[0])
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if len(candidates) != len(paths) {
			t.Fatalf("iteration %d: got %d candidates, want %d", i, len(candidates), len(paths))
		}
		if candidates[0] == paths[0] {
			t.Fatalf("iteration %d drew the last-served file %q first", i, paths[0])
		}
		if last := candidates[len(candidates)-1]; last != paths[0] {
			t.Fatalf("iteration %d: last candidate is %q, want the last-served %q", i, last, paths[0])
		}
	}
}

// The no-repeat memory belongs to the source, not to the package: a second
// source over the same directory starts with none, which is what keeps a
// fallback to disk from being judged against a file some other source served.
// Two files alternate, because each draw pushes the one just served to the back.
func TestLocalDirSourceRemembersWhatItServed(t *testing.T) {
	dir := t.TempDir()
	writeTempPNG(t, dir, "a.png", gradientRGBA(8, 8))
	writeTempPNG(t, dir, "b.png", gradientRGBA(8, 8))

	src := &localDirSource{dir: dir}
	_, previous, err := src.NextImage(context.Background())
	if err != nil {
		t.Fatalf("NextImage: %v", err)
	}
	for i := 0; i < 10; i++ {
		_, name, err := src.NextImage(context.Background())
		if err != nil {
			t.Fatalf("NextImage %d: %v", i, err)
		}
		if name == previous {
			t.Fatalf("draw %d repeated %q back-to-back", i, name)
		}
		previous = name
	}

	if fresh := (&localDirSource{dir: dir}).lastServed(); fresh != "" {
		t.Errorf("a new source remembered %q; the memory must not be shared", fresh)
	}
}

// Avoiding a repeat must never mean serving nothing: a one-image directory
// keeps serving its one image.
func TestImageCandidatesSingleFileStillServedAfterBeingServed(t *testing.T) {
	dir := t.TempDir()
	only := filepath.Join(dir, "only.png")
	if err := writeFile(only, "x"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if got := drawOne(t, dir, only); got != only {
			t.Fatalf("iteration %d: got %q, want %q", i, got, only)
		}
	}
}

// Documents the current posture: no authentication middleware is wired into
// newRouter, so no endpoint is authenticated — the firmware's "Server Auth
// Header" is sent but never read, and enforcing it is a reverse proxy's job
// (see README.md). This exercises the production router, so wiring auth in
// would fail here rather than pass silently.
func TestRoutesAreUnauthenticated(t *testing.T) {
	for _, ep := range []string{"/fetchImage", "/calibrationImage", "/clearImage"} {
		req := httptest.NewRequest(http.MethodPost, ep, strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		newRouter().ServeHTTP(rec, req)
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("%s is authenticated; the endpoints are expected to be open today", ep)
		}
	}
}

// The routing table is the one main() serves, so a route added, removed or
// renamed in newRouter shows up here.
func TestRouterExposesExactlyTheThreeEndpoints(t *testing.T) {
	want := map[string]bool{
		"POST /fetchImage":       true,
		"POST /calibrationImage": true,
		"POST /clearImage":       true,
	}

	got := map[string]bool{}
	err := chi.Walk(newRouter(), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		got[method+" "+route] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walking the router: %v", err)
	}

	for route := range want {
		if !got[route] {
			t.Errorf("route %q is missing from the router", route)
		}
	}
	for route := range got {
		if !want[route] {
			t.Errorf("router exposes an unexpected route %q", route)
		}
	}
}

// ===========================================================================
// Command line
// ===========================================================================

// service.nix interpolates `server <port> <imgDir>` by position, so the
// positional form has to keep working exactly as it did before -bind existed —
// and -bind has to be usable in front of it.
func TestParseArgs(t *testing.T) {
	const (
		defPort = "8080"
		defDir  = "./img"
	)
	cases := []struct {
		name               string
		args               []string
		bind, port, imgDir string
	}{
		{"no arguments", nil, "0.0.0.0", defPort, defDir},
		{"port only", []string{"18450"}, "0.0.0.0", "18450", defDir},
		{"the service.nix form", []string{"18450", "/var/lib/stillframe-server/images"}, "0.0.0.0", "18450", "/var/lib/stillframe-server/images"},
		{"bind before the positionals", []string{"-bind", "127.0.0.1", "18450", "/imgs"}, "127.0.0.1", "18450", "/imgs"},
		{"bind as --flag=value", []string{"--bind=100.64.0.1", "18450"}, "100.64.0.1", "18450", defDir},
		{"a directory with a space", []string{"18450", "/srv/my photos"}, "0.0.0.0", "18450", "/srv/my photos"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bind, port, imgDir, err := parseArgs(c.args, defPort, defDir)
			if err != nil {
				t.Fatalf("parseArgs(%q): %v", c.args, err)
			}
			if bind != c.bind || port != c.port || imgDir != c.imgDir {
				t.Errorf("got bind=%q port=%q imgDir=%q, want %q/%q/%q",
					bind, port, imgDir, c.bind, c.port, c.imgDir)
			}
		})
	}
}
