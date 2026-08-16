package main

import (
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
	silenceStdout(t)
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

func TestClearImageHandlerReturnsUniformColor(t *testing.T) {
	silenceStdout(t)
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

func TestClearImageHandlerColorIsNotWhite_KnownBug(t *testing.T) {
	t.Skip("KNOWN BUG: /clearImage returns colorSpace[3] (einkimage.go:111), which is blue " +
		"on the 7-color panel rather than white. See " +
		"TestGenerateClearImageShouldBeWhite_KnownBug.")

	silenceStdout(t)
	rec := postJSON(t, "/clearImage", ImageProperties{Width: 8, Height: 4, ColorSpace: epd7in3fPalette})
	white := epd7in3fPalette[1].Code
	if want := white<<4 | white; rec.Body.Bytes()[0] != want {
		t.Errorf("got %#x, want %#x", rec.Body.Bytes()[0], want)
	}
}

func TestHandlersRejectMalformedJSON(t *testing.T) {
	silenceStdout(t)
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

// An empty color_space makes GenerateCalibrationImage divide by zero. In
// production chi's Recoverer turns that into a 500 rather than taking the
// process down, which is what this asserts.
func TestCalibrationImageEmptyColorSpaceIsRecovered(t *testing.T) {
	silenceStdout(t)
	silenceStderr(t)
	rec := postJSON(t, "/calibrationImage", ImageProperties{Width: 8, Height: 4, ColorSpace: ColorSpace{}})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500 (a panic recovered by chi middleware)", rec.Code)
	}
}

func TestCalibrationImageEmptyColorSpace_KnownBug(t *testing.T) {
	t.Skip("KNOWN BUG: an untrusted request body with an empty (or missing) color_space " +
		"panics the handler - GenerateCalibrationImage divides by sideLength == 0 " +
		"(einkimage.go:91). /clearImage panics too, with index-out-of-range on " +
		"colorSpace[3] (einkimage.go:115). Only chi's Recoverer keeps the server alive. " +
		"The handlers should validate width/height/color_space and answer 400.")

	silenceStdout(t)
	rec := postJSON(t, "/calibrationImage", ImageProperties{Width: 8, Height: 4, ColorSpace: ColorSpace{}})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", rec.Code)
	}
}

func TestClearImageEmptyColorSpaceIsRecovered(t *testing.T) {
	silenceStdout(t)
	silenceStderr(t)
	rec := postJSON(t, "/clearImage", ImageProperties{Width: 8, Height: 4, ColorSpace: ColorSpace{}})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500 (a panic recovered by chi middleware)", rec.Code)
	}
}

func TestCalibrationImageZeroDimensions(t *testing.T) {
	silenceStdout(t)
	rec := postJSON(t, "/calibrationImage", ImageProperties{Width: 0, Height: 0, ColorSpace: epd7in3fPalette})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Body.Len(); got != 0 {
		t.Errorf("got %d bytes, want 0", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "0" {
		t.Errorf("Content-Length %q, want 0", got)
	}
}

func TestCalibrationImageAllShippedPalettes(t *testing.T) {
	silenceStdout(t)
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
	silenceStdout(t)
	imgDir := t.TempDir()
	writeTempPNG(t, imgDir, "only.png", gradientRGBA(64, 48))
	withImageDir(t, imgDir)
	stubSmartcrop(t, 0, 0, 64, 48)

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

func TestFetchImageIsDeterministicForAGivenImage(t *testing.T) {
	silenceStdout(t)
	imgDir := t.TempDir()
	writeTempPNG(t, imgDir, "only.png", gradientRGBA(64, 48))
	withImageDir(t, imgDir)
	stubSmartcrop(t, 0, 0, 64, 48)

	props := ImageProperties{Width: 16, Height: 8, ColorSpace: epd7in3fPalette}
	first := postJSON(t, "/fetchImage", props).Body.String()
	for i := 0; i < 3; i++ {
		if got := postJSON(t, "/fetchImage", props).Body.String(); got != first {
			t.Fatalf("fetchImage is not deterministic for a single-image directory (run %d)", i)
		}
	}
}

func TestFetchImageHonoursFlips(t *testing.T) {
	silenceStdout(t)
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
	stubSmartcrop(t, 0, 0, 64, 48)

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
	silenceStdout(t)
	imgDir := t.TempDir()
	src := solidRGBA(64, 48, color.RGBA{255, 255, 255, 255})
	for y := 0; y < 48; y++ {
		for x := 32; x < 64; x++ {
			src.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
		}
	}
	writeTempPNG(t, imgDir, "only.png", src)
	withImageDir(t, imgDir)
	stubSmartcrop(t, 0, 0, 64, 48)

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

// Pins the silent-failure path described in
// TestGetBestPieceOfImageWithoutSmartcrop_KnownBug: the device gets a 200 with
// an empty body instead of an error.
func TestFetchImageWithoutSmartcropReturns200AndNoBytes(t *testing.T) {
	silenceStdout(t)
	imgDir := t.TempDir()
	writeTempPNG(t, imgDir, "only.png", gradientRGBA(64, 48))
	withImageDir(t, imgDir)
	t.Setenv("PATH", t.TempDir())
	if _, err := exec.LookPath("smartcrop-cli"); err == nil {
		t.Skip("smartcrop-cli is still resolvable")
	}

	rec := postJSON(t, "/fetchImage", ImageProperties{Width: 16, Height: 8, ColorSpace: epd7in3fPalette})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("expected an empty body, got %d bytes", rec.Body.Len())
	}
}

func TestFetchImageWithoutSmartcrop_KnownBug(t *testing.T) {
	t.Skip("KNOWN BUG: /fetchImage answers 200 OK with Content-Length: 0 when smartcrop-cli " +
		"is unavailable (bestcrop.go:90 discards the error). The frame has no way to tell " +
		"a blank image from a broken server; it should be a 5xx.")

	silenceStdout(t)
	imgDir := t.TempDir()
	writeTempPNG(t, imgDir, "only.png", gradientRGBA(64, 48))
	withImageDir(t, imgDir)
	t.Setenv("PATH", t.TempDir())

	rec := postJSON(t, "/fetchImage", ImageProperties{Width: 16, Height: 8, ColorSpace: epd7in3fPalette})
	if rec.Code < 500 {
		t.Errorf("status %d, want a 5xx", rec.Code)
	}
}

func TestFetchImageNonImageFile_KnownBug(t *testing.T) {
	t.Skip("KNOWN BUG: a non-image file in the image directory makes ReadImage return a " +
		"nil image.Image (image.go:20 drops the decode error); flipImage then dereferences " +
		"it and the request panics. Recovered by chi's Recoverer in production, but the " +
		"file is picked again at random forever. See TestReadImageUndecodable_KnownBug.")

	silenceStdout(t)
	imgDir := t.TempDir()
	if err := writeFile(filepath.Join(imgDir, "readme.txt"), "not an image"); err != nil {
		t.Fatal(err)
	}
	withImageDir(t, imgDir)
	stubSmartcrop(t, 0, 0, 8, 8)

	rec := postJSON(t, "/fetchImage", ImageProperties{Width: 16, Height: 8, ColorSpace: epd7in3fPalette})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d", rec.Code)
	}
}

// ===========================================================================
// getRandomFile
// ===========================================================================

func TestGetRandomFileReturnsAFileFromTheDirectory(t *testing.T) {
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
		got, err := getRandomFile(dir)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if !want[got] {
			t.Fatalf("returned %q which is not one of the files in the directory", got)
		}
		seen[got] = true
	}
	if len(seen) < 2 {
		t.Errorf("expected the selection to vary across 200 draws, only saw %v", seen)
	}
}

func TestGetRandomFileMissingDirectory(t *testing.T) {
	if _, err := getRandomFile(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("expected an error for a missing directory")
	}
}

func TestGetRandomFileEmptyDirectoryPanicsToday(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected rand.Intn(0) to panic on an empty directory (current behaviour); got none")
		}
	}()
	_, _ = getRandomFile(t.TempDir())
}

func TestGetRandomFileEmptyDirectory_KnownBug(t *testing.T) {
	t.Skip("KNOWN BUG: main.go:33 calls rand.Intn(len(files)) without checking for an empty " +
		"directory, so an empty image directory panics with 'invalid argument to Intn'. " +
		"It should return an error.")

	if _, err := getRandomFile(t.TempDir()); err == nil {
		t.Error("expected an error for an empty directory")
	}
}

func TestGetRandomFileWithSubdirectories_KnownBug(t *testing.T) {
	t.Skip("KNOWN BUG: main.go:33-42 draws n from the count of ALL directory entries but " +
		"only decrements n for regular files, so whenever the image directory contains " +
		"sub-directories the function frequently falls through the loop and returns " +
		"os.ErrNotExist. fetchImage (main.go:101) ignores that error and passes \"\" to " +
		"ReadImage, which calls os.Exit(1) (image.go:17) - a single stray sub-directory " +
		"can therefore take the whole server process down. n should be drawn from the " +
		"number of regular files.")

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
		got, err := getRandomFile(dir)
		if err != nil {
			t.Fatalf("iteration %d: %v (the only regular file is %q)", i, err, only)
		}
		if got != only {
			t.Fatalf("iteration %d: got %q, want %q", i, got, only)
		}
	}
}

// ===========================================================================
// basicAuth middleware (currently not wired up in main())
// ===========================================================================

func TestBasicAuthRejectsMissingCredentials(t *testing.T) {
	h := basicAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	req := httptest.NewRequest(http.MethodPost, "/fetchImage", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", rec.Code)
	}
}

func TestBasicAuthRejectsWrongCredentials(t *testing.T) {
	h := basicAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	for _, c := range [][2]string{{"username", "wrong"}, {"wrong", "password"}, {"", ""}} {
		req := httptest.NewRequest(http.MethodPost, "/fetchImage", nil)
		req.SetBasicAuth(c[0], c[1])
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%v: status %d, want 401", c, rec.Code)
		}
	}
}

func TestBasicAuthAcceptsHardcodedCredentials(t *testing.T) {
	// NOTE: the credentials are hard-coded to username/password in main.go:119.
	// The middleware is deliberately not wired up (main.go:146 comments out
	// r.Use(basicAuth)), so this only documents the helper.
	h := basicAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	req := httptest.NewRequest(http.MethodPost, "/fetchImage", nil)
	req.SetBasicAuth("username", "password")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("status %d body %q, want 200 'ok'", rec.Code, rec.Body.String())
	}
}

func TestRoutesAreUnauthenticated(t *testing.T) {
	// Documents the current posture: no auth is applied to any endpoint.
	silenceStdout(t)
	rec := postJSON(t, "/calibrationImage", ImageProperties{Width: 8, Height: 4, ColorSpace: epd7in3fPalette})
	if rec.Code == http.StatusUnauthorized {
		t.Error("expected the endpoints to be unauthenticated today")
	}
}
