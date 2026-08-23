package main

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ===========================================================================
// ProcessFindingCrop
//
// This is the seam that isolates bestcrop.go from the external Python program:
// it takes the processing function as a parameter, so it can be exercised with
// a plain Go callback and no subprocess at all.
// ===========================================================================

func TestProcessFindingCropWritesDecodablePNG(t *testing.T) {
	src := gradientRGBA(9, 6)

	var seenPath string
	var seenW, seenH int
	var decoded image.Image

	rect, err := ProcessFindingCrop(context.Background(), src, 40, 20, func(_ context.Context, path string, w, h int) (image.Rectangle, error) {
		seenPath, seenW, seenH = path, w, h

		f, ferr := os.Open(path)
		if ferr != nil {
			return image.Rectangle{}, ferr
		}
		defer f.Close()
		img, derr := png.Decode(f)
		if derr != nil {
			return image.Rectangle{}, derr
		}
		decoded = img
		return image.Rect(1, 2, 5, 6), nil
	})
	if err != nil {
		t.Fatalf("ProcessFindingCrop: %v", err)
	}

	if rect != image.Rect(1, 2, 5, 6) {
		t.Errorf("returned rect %v, want (1,2)-(5,6)", rect)
	}
	if seenW != 40 || seenH != 20 {
		t.Errorf("processFunc got %dx%d, want 40x20", seenW, seenH)
	}
	if !strings.HasSuffix(seenPath, ".png") {
		t.Errorf("temp file %q should be named *.png so decoders can sniff it", seenPath)
	}
	if decoded == nil {
		t.Fatal("temp file did not decode as PNG")
	}
	if decoded.Bounds() != src.Bounds() {
		t.Errorf("encoded image bounds %v, want %v", decoded.Bounds(), src.Bounds())
	}
	// Spot-check that the pixels actually survived the round trip.
	for _, p := range [][2]int{{0, 0}, {8, 5}, {4, 3}} {
		r1, g1, b1, _ := decoded.At(p[0], p[1]).RGBA()
		r2, g2, b2, _ := src.At(p[0], p[1]).RGBA()
		if r1 != r2 || g1 != g2 || b1 != b2 {
			t.Errorf("pixel %v differs after PNG round trip", p)
		}
	}
}

// The temp PNG's coordinate space always starts at (0,0), but the input image's
// bounds may not (a GIF first frame anchored at its frame offset, a sub-image).
// The returned rectangle must be translated into the image's own space, or the
// caller's SubImage would select the wrong region — or an empty one.
func TestProcessFindingCropTranslatesToImageSpace(t *testing.T) {
	src := image.NewRGBA(image.Rect(100, 50, 300, 200)) // 200x150 at (100,50)

	rect, err := ProcessFindingCrop(context.Background(), src, 40, 20, func(_ context.Context, path string, w, h int) (image.Rectangle, error) {
		// The cropper answers in the PNG's 0-based space.
		return image.Rect(10, 20, 90, 80), nil
	})
	if err != nil {
		t.Fatalf("ProcessFindingCrop: %v", err)
	}
	if want := image.Rect(110, 70, 190, 130); rect != want {
		t.Errorf("returned rect %v, want %v (translated by the image's origin)", rect, want)
	}
}

func TestGetBestPieceOfImageHonoursTheCropRectOnNonZeroOrigin(t *testing.T) {
	silenceStdout(t)
	// A 32x16 image anchored at (100,50): left half red, right half blue.
	src := image.NewRGBA(image.Rect(100, 50, 132, 66))
	for y := 50; y < 66; y++ {
		for x := 100; x < 132; x++ {
			if x < 116 {
				src.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
			} else {
				src.SetRGBA(x, y, color.RGBA{0, 0, 255, 255})
			}
		}
	}
	// The stub answers in temp-PNG space: the right (blue) half is (16,0)-(32,16).
	stubSmartcrop(t, 16, 0, 16, 16)

	got := GetBestPieceOfImage(context.Background(), 8, 8, src)
	if got.Bounds() != image.Rect(0, 0, 8, 8) {
		t.Fatalf("got %v, want an 8x8 image", got.Bounds())
	}
	r, g, b, _ := got.At(4, 4).RGBA()
	if b <= r || g > 0x2000 {
		t.Errorf("cropped region should be the blue half, got r=%d g=%d b=%d", r, g, b)
	}
}

func TestProcessFindingCropCleansUpOnSuccess(t *testing.T) {
	var tmpFile, tmpDir string
	_, err := ProcessFindingCrop(context.Background(), gradientRGBA(4, 4), 10, 10, func(_ context.Context, path string, w, h int) (image.Rectangle, error) {
		tmpFile = path
		tmpDir = filepath.Dir(path)
		if _, statErr := os.Stat(path); statErr != nil {
			t.Errorf("temp file should exist while processFunc runs: %v", statErr)
		}
		return image.Rect(0, 0, 1, 1), nil
	})
	if err != nil {
		t.Fatalf("ProcessFindingCrop: %v", err)
	}
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Errorf("temp file %q was not removed", tmpFile)
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Errorf("temp dir %q was not removed", tmpDir)
	}
}

func TestProcessFindingCropPropagatesProcessError(t *testing.T) {
	sentinel := errors.New("boom")
	rect, err := ProcessFindingCrop(context.Background(), gradientRGBA(4, 4), 10, 10, func(context.Context, string, int, int) (image.Rectangle, error) {
		return image.Rectangle{}, sentinel
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error should wrap the processFunc error, got %v", err)
	}
	if !strings.Contains(err.Error(), "processing image") {
		t.Errorf("error should be annotated with context, got %q", err)
	}
	if rect != (image.Rectangle{}) {
		t.Errorf("expected the zero rectangle on error, got %v", rect)
	}
}

// Cleanup is deferred, so a failed crop - which is every request when
// smartcrop-cli is missing or errors - must not leave a full-size PNG behind in
// TMPDIR.
func TestProcessFindingCropCleansUpOnError(t *testing.T) {
	var tmpDir string
	_, _ = ProcessFindingCrop(context.Background(), gradientRGBA(4, 4), 10, 10, func(_ context.Context, path string, w, h int) (image.Rectangle, error) {
		tmpDir = filepath.Dir(path)
		return image.Rectangle{}, errors.New("boom")
	})
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Errorf("temp dir %q was leaked after a processing error", tmpDir)
	}
}

// ===========================================================================
// pythonSmartcrop
//
// Exercised against a shell-script stub named `smartcrop-cli` placed on PATH,
// so no Python or smartcrop package is required.
// ===========================================================================

func TestPythonSmartcropParsesOutput(t *testing.T) {
	stub := stubSmartcrop(t, 12, 34, 100, 60)

	rect, err := pythonSmartcrop(context.Background(), "/some/image.png", 800, 480)
	if err != nil {
		t.Fatalf("pythonSmartcrop: %v", err)
	}
	// The JSON gives an origin plus a size; the rect must be origin..origin+size.
	if want := image.Rect(12, 34, 112, 94); rect != want {
		t.Errorf("got %v, want %v", rect, want)
	}

	args := stub.recordedArgs(t)
	if len(args) != 1 {
		t.Fatalf("expected exactly one invocation, got %v", args)
	}
	if args[0] != "/some/image.png 800 480" {
		t.Errorf("smartcrop-cli was called with %q, want %q", args[0], "/some/image.png 800 480")
	}
}

func TestPythonSmartcropReportsExecFailure(t *testing.T) {
	stubSmartcropRaw(t, "", 3) // non-zero exit
	_, err := pythonSmartcrop(context.Background(), "/some/image.png", 800, 480)
	if err == nil {
		t.Fatal("expected an error when smartcrop-cli exits non-zero")
	}
	if !strings.Contains(err.Error(), "running python script") {
		t.Errorf("got %q, want a 'running python script' annotation", err)
	}
}

func TestPythonSmartcropReportsBadJSON(t *testing.T) {
	stubSmartcropRaw(t, "not json at all", 0)
	_, err := pythonSmartcrop(context.Background(), "/some/image.png", 800, 480)
	if err == nil {
		t.Fatal("expected an error for unparseable output")
	}
	if !strings.Contains(err.Error(), "parsing output") {
		t.Errorf("got %q, want a 'parsing output' annotation", err)
	}
}

func TestPythonSmartcropMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // nothing on PATH
	if _, err := exec.LookPath("smartcrop-cli"); err == nil {
		t.Skip("smartcrop-cli is still resolvable; cannot test the missing-binary path")
	}
	_, err := pythonSmartcrop(context.Background(), "/some/image.png", 800, 480)
	if err == nil {
		t.Fatal("expected an error when smartcrop-cli is not installed")
	}
}

// hangForever is a smartcrop-cli body that never returns. `exec` matters: a
// forked child would keep the inherited stdout pipe open after the shell is
// killed and cmd.Output() would block on it anyway. The real smartcrop-cli is
// a makeWrapper script, which execs python for the same reason.
const hangForever = "exec sleep 60"

// stubSmartcropShell installs an arbitrary shell script as smartcrop-cli, for
// the failure modes the JSON-printing stub cannot express (writing to stderr,
// hanging forever).
func stubSmartcropShell(t *testing.T, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub is POSIX only")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "smartcrop-cli")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Only the child's stderr says *why* the crop failed. Discarding it left the
// operator with "exit status 1" while every image silently center-cropped -
// which is exactly how a Pillow incompatibility inside smartcrop.py can hide.
func TestPythonSmartcropReportsStderr(t *testing.T) {
	stubSmartcropShell(t, "echo 'AttributeError: no attribute ANTIALIAS' >&2\nexit 1")

	_, err := pythonSmartcrop(context.Background(), "/some/image.png", 800, 480)
	if err == nil {
		t.Fatal("expected an error when smartcrop-cli exits non-zero")
	}
	if !strings.Contains(err.Error(), "AttributeError: no attribute ANTIALIAS") {
		t.Errorf("got %q, want the child's stderr included", err)
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Errorf("got %q, want the exit status kept as well", err)
	}
}

// A wedged interpreter must not pin the handler goroutine forever. The
// production limit is smartcropTimeout; a shorter deadline on the caller's
// context wins, which is what makes this testable in milliseconds.
func TestPythonSmartcropTimesOut(t *testing.T) {
	stubSmartcropShell(t, hangForever)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := pythonSmartcrop(ctx, "/some/image.png", 800, 480)
	if err == nil {
		t.Fatal("expected an error when smartcrop-cli hangs")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("got %q, want a 'timed out' annotation", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Errorf("waited %s for a hung child; the context was ignored", elapsed)
	}
}

// Killing the child does not close the stdout pipe if it left a grandchild
// holding it, and Output() waits for that pipe. Without cmd.WaitDelay this
// returned only when the grandchild did - 30s here, unbounded in general.
func TestPythonSmartcropSurvivesAnUnkillableGrandchild(t *testing.T) {
	stubSmartcropShell(t, "sleep 30 & wait")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := pythonSmartcrop(ctx, "/some/image.png", 800, 480)
	if err == nil {
		t.Fatal("expected an error when smartcrop-cli hangs")
	}
	if elapsed := time.Since(started); elapsed > waitDelay+10*time.Second {
		t.Errorf("waited %s; the pipe copy was not bounded by waitDelay (%s)", elapsed, waitDelay)
	}
}

func TestPythonSmartcropReportsCancellation(t *testing.T) {
	stubSmartcropShell(t, hangForever)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	defer cancel()

	_, err := pythonSmartcrop(ctx, "/some/image.png", 800, 480)
	if err == nil {
		t.Fatal("expected an error when the caller goes away")
	}
	if !strings.Contains(err.Error(), "canceled") {
		t.Errorf("got %q, want a 'canceled' annotation", err)
	}
}

func TestStderrDetail(t *testing.T) {
	if got := stderrDetail(errors.New("boom")); got != "" {
		t.Errorf("a non-ExitError has no stderr, got %q", got)
	}
	long := strings.Repeat("a", stderrTail+100) + "THE-EXCEPTION"
	got := stderrDetail(&exec.ExitError{Stderr: []byte("  " + long + "\n")})
	if !strings.HasSuffix(got, "THE-EXCEPTION") {
		t.Errorf("the tail of a long traceback must survive, got %q", got)
	}
	if len(got) > stderrTail+8 {
		t.Errorf("stderr was not capped: %d bytes", len(got))
	}
}

func TestPythonResultJSONShape(t *testing.T) {
	// The Python side prints smartcrop's top_crop dict; pin the field names.
	var got PythonResult
	if err := json.Unmarshal([]byte(`{"x":1,"y":2,"width":3,"height":4,"score":{"total":0.5}}`), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != (PythonResult{X: 1, Y: 2, Width: 3, Height: 4}) {
		t.Errorf("got %+v", got)
	}
}

// ===========================================================================
// GetBestPieceOfImage
// ===========================================================================

func TestGetBestPieceOfImageResizesToRequestedSize(t *testing.T) {
	silenceStdout(t)
	stubSmartcrop(t, 0, 0, 64, 48)

	src := gradientRGBA(64, 48)
	got := GetBestPieceOfImage(context.Background(), 32, 24, src)
	if got.Bounds() != image.Rect(0, 0, 32, 24) {
		t.Errorf("got %v, want a 32x24 image anchored at the origin", got.Bounds())
	}
}

func TestGetBestPieceOfImageHonoursTheCropRect(t *testing.T) {
	silenceStdout(t)
	// Left half solid red, right half solid blue; crop only the right half.
	src := image.NewRGBA(image.Rect(0, 0, 32, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 32; x++ {
			if x < 16 {
				src.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
			} else {
				src.SetRGBA(x, y, color.RGBA{0, 0, 255, 255})
			}
		}
	}
	stubSmartcrop(t, 16, 0, 16, 16)

	got := GetBestPieceOfImage(context.Background(), 8, 8, src)
	if got.Bounds() != image.Rect(0, 0, 8, 8) {
		t.Fatalf("got %v, want an 8x8 image", got.Bounds())
	}
	r, g, b, _ := got.At(4, 4).RGBA()
	if b <= r || g > 0x2000 {
		t.Errorf("cropped region should be the blue half, got r=%d g=%d b=%d", r, g, b)
	}
}

// When smartcrop-cli is missing or fails, GetBestPieceOfImage falls back to a
// centered crop of the requested aspect ratio rather than propagating the zero
// rectangle (which resized to a 0x0 image and made /fetchImage answer 200 with
// an empty body; the firmware detects the short transfer and keeps the old
// image, so the failure was invisible on the frame and in the response).
func TestGetBestPieceOfImageWithoutSmartcropFallsBackToCenterCrop(t *testing.T) {
	silenceStdout(t)
	t.Setenv("PATH", t.TempDir())
	if _, err := exec.LookPath("smartcrop-cli"); err == nil {
		t.Skip("smartcrop-cli is still resolvable")
	}

	got := GetBestPieceOfImage(context.Background(), 32, 24, gradientRGBA(64, 48))
	if got.Bounds() != image.Rect(0, 0, 32, 24) {
		t.Errorf("a failed crop should still yield a full-size image, got %v", got.Bounds())
	}
}

func TestCenterCrop(t *testing.T) {
	cases := []struct {
		name          string
		bounds        image.Rectangle
		width, height int
		want          image.Rectangle
	}{
		{"same aspect ratio uses the whole image", image.Rect(0, 0, 64, 48), 32, 24, image.Rect(0, 0, 64, 48)},
		{"source wider than target crops the sides", image.Rect(0, 0, 100, 50), 1, 1, image.Rect(25, 0, 75, 50)},
		{"source taller than target crops top and bottom", image.Rect(0, 0, 50, 100), 1, 1, image.Rect(0, 25, 50, 75)},
		{"honours a non-zero origin", image.Rect(10, 20, 110, 70), 1, 1, image.Rect(35, 20, 85, 70)},
		{"degenerate source is returned unchanged", image.Rect(0, 0, 0, 0), 8, 8, image.Rect(0, 0, 0, 0)},
		{"degenerate target is returned unchanged", image.Rect(0, 0, 8, 8), 0, 0, image.Rect(0, 0, 8, 8)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := centerCrop(tc.bounds, tc.width, tc.height); got != tc.want {
				t.Errorf("centerCrop(%v, %d, %d) = %v, want %v", tc.bounds, tc.width, tc.height, got, tc.want)
			}
		})
	}
}

// The fallback must keep the middle of the picture, not a corner.
func TestGetBestPieceOfImageFallbackKeepsTheCenter(t *testing.T) {
	silenceStdout(t)
	t.Setenv("PATH", t.TempDir())
	if _, err := exec.LookPath("smartcrop-cli"); err == nil {
		t.Skip("smartcrop-cli is still resolvable")
	}

	// A wide image: red edges, blue middle third. A square crop keeps the blue.
	src := image.NewRGBA(image.Rect(0, 0, 90, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 90; x++ {
			if x >= 30 && x < 60 {
				src.SetRGBA(x, y, color.RGBA{0, 0, 255, 255})
			} else {
				src.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
			}
		}
	}

	got := GetBestPieceOfImage(context.Background(), 8, 8, src)
	r, _, b, _ := got.At(4, 4).RGBA()
	if b <= r {
		t.Errorf("the centered fallback crop should keep the blue middle, got r=%d b=%d", r, b)
	}
}

// A hung cropper must cost one timeout, not the request: the frame still gets a
// picture, from the centered fallback, and the temp PNG is still cleaned up.
func TestGetBestPieceOfImageFallsBackWhenSmartcropHangs(t *testing.T) {
	silenceStdout(t)
	stubSmartcropShell(t, hangForever)

	// A wide image: red edges, blue middle third. A square crop keeps the blue.
	src := image.NewRGBA(image.Rect(0, 0, 90, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 90; x++ {
			if x >= 30 && x < 60 {
				src.SetRGBA(x, y, color.RGBA{0, 0, 255, 255})
			} else {
				src.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
			}
		}
	}

	// Own the temp dir so the leak check sees only this crop's droppings.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	got := GetBestPieceOfImage(ctx, 8, 8, src)
	if got.Bounds() != image.Rect(0, 0, 8, 8) {
		t.Fatalf("got %v, want an 8x8 image", got.Bounds())
	}
	r, _, b, _ := got.At(4, 4).RGBA()
	if b <= r {
		t.Errorf("the centered fallback crop should keep the blue middle, got r=%d b=%d", r, b)
	}
	left, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("the hung crop leaked %d entries in TMPDIR: %v", len(left), left)
	}
}

// The cropper answers in whole pixels, so its rectangle is never exactly the
// panel's aspect ratio; the exact-size resize would turn the difference into an
// anisotropic stretch, so the rect is snapped before it is used.
func TestSnapToAspect(t *testing.T) {
	bounds := image.Rect(0, 0, 1000, 1000)
	cases := []struct {
		name          string
		r             image.Rectangle
		width, height int
		want          image.Rectangle
	}{
		// 605x809 is what the Python cropper's rounding produces for a
		// 1200x1600 panel: 0.17% narrow, snapped down to an exact 3:4.
		{"rounds a slightly-off ratio down", image.Rect(0, 0, 605, 809), 1200, 1600, image.Rect(0, 1, 605, 807)},
		{"leaves an exact ratio alone", image.Rect(10, 20, 610, 820), 600, 800, image.Rect(10, 20, 610, 820)},
		{"clamps an overhanging rect", image.Rect(500, 500, 2000, 2000), 1, 1, image.Rect(500, 500, 1000, 1000)},
		{"a rect off the image degrades to the centered crop", image.Rect(2000, 2000, 3000, 3000), 2, 1, image.Rect(0, 250, 1000, 750)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := snapToAspect(tc.r, bounds, tc.width, tc.height)
			if got != tc.want {
				t.Errorf("snapToAspect(%v, %v, %d, %d) = %v, want %v", tc.r, bounds, tc.width, tc.height, got, tc.want)
			}
			if got.Empty() || !got.In(bounds) {
				t.Fatalf("%v is not a usable region of %v", got, bounds)
			}
			// Whole-pixel crops cannot always hit the ratio dead on, but the
			// residual must be far below the 0.29% the raw cropper rectangle
			// carries - that is what shows up as a stretch after the resize.
			if e := aspectError(got, tc.width, tc.height); e > 0.002 {
				t.Errorf("%v is %d:%d, %.3f%% off the requested %d:%d", got, got.Dx(), got.Dy(), e*100, tc.width, tc.height)
			}
		})
	}
}

// aspectError is how far r's aspect ratio is from width:height, as a fraction.
func aspectError(r image.Rectangle, width, height int) float64 {
	want := float64(width) / float64(height)
	got := float64(r.Dx()) / float64(r.Dy())
	return math.Abs(got-want) / want
}

func TestGetBestPieceOfImageClampsOversizedCropRect(t *testing.T) {
	silenceStdout(t)
	// The stub asks for a region larger than the source; SubImage intersects it
	// with the source bounds, so this must not panic.
	stubSmartcrop(t, 0, 0, 999, 999)
	got := GetBestPieceOfImage(context.Background(), 16, 8, gradientRGBA(32, 32))
	if got.Bounds() != image.Rect(0, 0, 16, 8) {
		t.Errorf("got %v, want a 16x8 image", got.Bounds())
	}
}
