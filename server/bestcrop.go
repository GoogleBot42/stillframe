package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strconv"

	"github.com/nfnt/resize"
)

// Writes the file as a PNG to a temporary file for the purpose of processing the image using another program
func ProcessFindingCrop(img image.Image, width, height int, processFunc func(string, int, int) (image.Rectangle, error)) (image.Rectangle, error) {
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

	// Use png.Encode to encode img to the PNG format with tmpFile as the target writer
	if err := png.Encode(tmpFile, img); err != nil {
		return image.Rectangle{}, fmt.Errorf("encoding image: %w", err)
	}

	// Close the file so the child process sees a complete PNG
	if err := tmpFile.Close(); err != nil {
		return image.Rectangle{}, fmt.Errorf("closing temp file: %w", err)
	}

	// Run the process function
	res, err := processFunc(tmpFile.Name(), width, height)
	if err != nil {
		return image.Rectangle{}, fmt.Errorf("processing image: %w", err)
	}

	return res, nil
}

type PythonResult struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Go's smartcrop selects terrible crops.  Of my test images it never selected the face.
// But the python smartcrop works great.  So save to a file and execute the python one instead.
func pythonSmartcrop(filename string, width, height int) (image.Rectangle, error) {
	cmd := exec.Command("smartcrop-cli", filename, strconv.Itoa(width), strconv.Itoa(height))

	output, err := cmd.Output()
	if err != nil {
		return image.Rectangle{}, fmt.Errorf("running python script: %w", err)
	}

	var result PythonResult
	err = json.Unmarshal(output, &result)
	if err != nil {
		return image.Rectangle{}, fmt.Errorf("parsing output: %w", err)
	}

	rect := image.Rect(result.X, result.Y, result.X+result.Width, result.Y+result.Height)
	return rect, nil
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

func GetBestPieceOfImage(width, height int, img image.Image) image.Image {
	// analyzer := smartcrop.NewAnalyzer(nfnt.NewDefaultResizer())
	// bestCrop, _ := analyzer.FindBestCrop(img, 800, 480)

	fmt.Println("Finding best image crop...")

	bestCrop, err := ProcessFindingCrop(img, width, height, pythonSmartcrop)
	if err != nil {
		log.Printf("warning: smart crop failed (%v); falling back to a centered crop", err)
		bestCrop = centerCrop(img.Bounds(), width, height)
	}

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
