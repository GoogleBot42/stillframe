package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/nfnt/resize"
)

// smartcropTimeout bounds a single smartcrop-cli run. Smart cropping a large
// photo on a slow SBC can legitimately take several seconds, so this is
// deliberately generous; past it the interpreter is wedged (PIL on a truncated
// file, a stalled network mount) and waiting forever would pin the handler
// goroutine, keep the full-size temp PNG on disk, and strand another Python
// process on every wake until the box runs out of memory or disk.
const smartcropTimeout = 30 * time.Second

// waitDelay bounds how long the pipe copy may outlive the killed child.
const waitDelay = 5 * time.Second

// stderrTail caps how much of a failed subprocess's output reaches the log.
const stderrTail = 2048

// Writes the file as a PNG to a temporary file for the purpose of processing the image using another program
func ProcessFindingCrop(ctx context.Context, img image.Image, width, height int, processFunc func(context.Context, string, int, int) (image.Rectangle, error)) (image.Rectangle, error) {
	// Encoding a 12 MP PNG is the expensive half of this function, so do not
	// start it for a request that has already gone away.
	if err := ctx.Err(); err != nil {
		return image.Rectangle{}, fmt.Errorf("crop abandoned: %w", err)
	}

	// Create a temporary directory
	tmpDir, err := ioutil.TempDir("", "image_processing")
	if err != nil {
		return image.Rectangle{}, fmt.Errorf("creating temp dir: %w", err)
	}
	// Clean up the temp file and directory on every exit path, not just success.
	defer os.RemoveAll(tmpDir)

	// Create a temporary file within our temp-directory that follows a particular naming pattern
	tmpFile, err := ioutil.TempFile(tmpDir, "image-*.png")
	if err != nil {
		return image.Rectangle{}, fmt.Errorf("creating temp file: %w", err)
	}
	defer tmpFile.Close()

	// Use png.Encode to encode img to the PNG format with tmpFile as the target
	// writer. Convert to RGBA first: the png encoder writes color models it does
	// not recognise — like the *image.YCbCr a JPEG decodes to — at 16 bits per
	// channel through a per-pixel conversion, which more than triples the encode
	// time at 12 MP. (imageToRGBA is a no-op for *image.RGBA input.)
	if err := png.Encode(tmpFile, imageToRGBA(img)); err != nil {
		return image.Rectangle{}, fmt.Errorf("encoding image: %w", err)
	}

	// Close the file so the child process sees a complete PNG
	if err := tmpFile.Close(); err != nil {
		return image.Rectangle{}, fmt.Errorf("closing temp file: %w", err)
	}

	// Run the process function
	res, err := processFunc(ctx, tmpFile.Name(), width, height)
	if err != nil {
		return image.Rectangle{}, fmt.Errorf("processing image: %w", err)
	}

	// The temp PNG's coordinate space starts at (0,0), but img's own bounds may
	// not (a GIF's first frame can be anchored at its frame offset, and a
	// sub-image keeps its parent's coordinates). Translate the result into img's
	// space so SubImage(res) selects the region the cropper actually chose.
	return res.Add(img.Bounds().Min), nil
}

type PythonResult struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Go's smartcrop selects terrible crops.  Of my test images it never selected the face.
// But the python smartcrop works great.  So save to a file and execute the python one instead.
func pythonSmartcrop(ctx context.Context, filename string, width, height int) (image.Rectangle, error) {
	ctx, cancel := context.WithTimeout(ctx, smartcropTimeout)
	defer cancel()

	started := time.Now()
	cmd := exec.CommandContext(ctx, "smartcrop-cli", filename, strconv.Itoa(width), strconv.Itoa(height))
	// Killing the child is not enough on its own: Output() also waits for the
	// stdout pipe to close, and any grandchild still holding it would keep the
	// handler blocked long past the deadline. Give the copy a hard stop too.
	cmd.WaitDelay = waitDelay

	output, err := cmd.Output()
	if err != nil {
		// The child's own diagnostics are the only thing that explains the
		// failure; without them the log reads "exit status 1" while every
		// image silently center-crops.
		detail := stderrDetail(err)
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return image.Rectangle{}, fmt.Errorf("running python script: timed out after %s (%w)%s", time.Since(started).Round(time.Millisecond), ctx.Err(), detail)
		case errors.Is(ctx.Err(), context.Canceled):
			return image.Rectangle{}, fmt.Errorf("running python script: canceled after %s (%w)%s", time.Since(started).Round(time.Millisecond), ctx.Err(), detail)
		}
		return image.Rectangle{}, fmt.Errorf("running python script: %w%s", err, detail)
	}

	var result PythonResult
	err = json.Unmarshal(output, &result)
	if err != nil {
		// Quote what could not be parsed - a cropper that prints a warning
		// before its JSON exits 0, so stderrDetail never sees it.
		return image.Rectangle{}, fmt.Errorf("parsing output %q: %w", truncate(string(output)), err)
	}

	rect := image.Rect(result.X, result.Y, result.X+result.Width, result.Y+result.Height)
	return rect, nil
}

// stderrDetail renders the stderr an *exec.ExitError carries as a log suffix,
// or "" when there is nothing to show.
func stderrDetail(err error) string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return ""
	}
	msg := truncate(strings.TrimSpace(string(exitErr.Stderr)))
	if msg == "" {
		return ""
	}
	return ": " + msg
}

// truncate keeps the last stderrTail bytes of a subprocess's output. The tail,
// because a Python traceback ends with the exception. The cut can land
// mid-rune, so scrub what it breaks rather than logging invalid UTF-8.
func truncate(s string) string {
	if len(s) <= stderrTail {
		return s
	}
	return "..." + strings.ToValidUTF8(s[len(s)-stderrTail:], "")
}

// centerCrop returns the largest centered sub-rectangle of bounds that has the
// requested aspect ratio. It is the fallback used when the smart cropper is
// unavailable: a frame with a broken cropper should still show photos.
func centerCrop(bounds image.Rectangle, width, height int) image.Rectangle {
	srcW, srcH := bounds.Dx(), bounds.Dy()
	if srcW <= 0 || srcH <= 0 || width <= 0 || height <= 0 {
		return bounds
	}

	cropW, cropH := srcW, srcW*height/width
	if cropH > srcH {
		cropW, cropH = srcH*width/height, srcH
	}
	if cropW < 1 {
		cropW = 1
	}
	if cropH < 1 {
		cropH = 1
	}

	x := bounds.Min.X + (srcW-cropW)/2
	y := bounds.Min.Y + (srcH-cropH)/2

	return image.Rect(x, y, x+cropW, y+cropH)
}

// snapToAspect confines a crop rectangle to the image and shrinks it to the
// panel's exact aspect ratio. The cropper answers in whole pixels, so its
// rectangle is always a fraction of a percent off the requested ratio, and the
// resize that follows is exact-size - the difference would come out as an
// anisotropic stretch. A rectangle that does not overlap the image at all (a
// confused cropper) degrades to a centered crop of the whole image, which is
// also what the fallback path asks for.
func snapToAspect(r, bounds image.Rectangle, width, height int) image.Rectangle {
	r = r.Intersect(bounds)
	if r.Empty() {
		r = bounds
	}
	return centerCrop(r, width, height)
}

func GetBestPieceOfImage(ctx context.Context, width, height int, img image.Image) image.Image {
	// analyzer := smartcrop.NewAnalyzer(nfnt.NewDefaultResizer())
	// bestCrop, _ := analyzer.FindBestCrop(img, 800, 480)

	fmt.Println("Finding best image crop...")

	bestCrop, err := ProcessFindingCrop(ctx, img, width, height, pythonSmartcrop)
	if err != nil {
		log.Printf("warning: smart crop failed (%v); falling back to a centered crop", err)
		bestCrop = img.Bounds()
	}

	bestCrop = snapToAspect(bestCrop, img.Bounds(), width, height)

	fmt.Printf("Best crop: %+v\n", bestCrop)

	type SubImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	croppedImg := img
	if subImager, ok := img.(SubImager); ok {
		croppedImg = subImager.SubImage(bestCrop)
	}

	resizedImg := resize.Resize(uint(width), uint(height), croppedImg, resize.Lanczos3)

	return resizedImg
}
