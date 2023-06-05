package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io/ioutil"
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

	// Create a temporary file within our temp-directory that follows a particular naming pattern
	tmpFile, err := ioutil.TempFile(tmpDir, "image-*.png")
	if err != nil {
		return image.Rectangle{}, fmt.Errorf("creating temp file: %w", err)
	}

	// Use png.Encode to encode img to the PNG format with tmpFile as the target writer
	if err := png.Encode(tmpFile, img); err != nil {
		return image.Rectangle{}, fmt.Errorf("encoding image: %w", err)
	}

	// Close the file
	if err := tmpFile.Close(); err != nil {
		return image.Rectangle{}, fmt.Errorf("closing temp file: %w", err)
	}

	// Run the process function
	res, err := processFunc(tmpFile.Name(), width, height)
	if err != nil {
		return image.Rectangle{}, fmt.Errorf("processing image: %w", err)
	}

	// Delete the temporary file and directory after the function completes
	if err := os.Remove(tmpFile.Name()); err != nil {
		return image.Rectangle{}, fmt.Errorf("deleting temp file: %w", err)
	}
	if err := os.Remove(tmpDir); err != nil {
		return image.Rectangle{}, fmt.Errorf("deleting temp dir: %w", err)
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

func GetBestPieceOfImage(width, height int, img image.Image) image.Image {
	// analyzer := smartcrop.NewAnalyzer(nfnt.NewDefaultResizer())
	// bestCrop, _ := analyzer.FindBestCrop(img, 800, 480)

	fmt.Println("Finding best image crop...")

	bestCrop, _ := ProcessFindingCrop(img, width, height, pythonSmartcrop)

	fmt.Printf("Best crop: %+v\n", bestCrop)

	type SubImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	croppedImg := img.(SubImager).SubImage(bestCrop)

	resizedImg := resize.Resize(800, 480, croppedImg, resize.Lanczos3)

	return resizedImg
}
