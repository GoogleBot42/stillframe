package main

import (
	"errors"
	"image"
	"math"

	"github.com/mattn/go-ciede2000"
)

// Maps RGB colors to the eink display's color values
type ColorSpace []struct {
	Color Colorf `json:"rgb_color"`
	Code  uint8  `json:"color_code"`
}

func ditherFloydSteinberg(image [][]Colorf, x int, y int, color Colorf) {
	ditherPixel := func(x int, y int, color Colorf) {
		height := len(image)
		width := len(image[0])

		if x < 0 || y < 0 || x >= width || y >= height {
			return
		}

		image[y][x] = clampColor(addColorf(image[y][x], color), 0, 1)
	}

	ditherPixel(x+1, y, multColor(color, 7.0/16.0))
	ditherPixel(x-1, y+1, multColor(color, 3.0/16.0))
	ditherPixel(x, y+1, multColor(color, 5.0/16.0))
	ditherPixel(x+1, y+1, multColor(color, 1.0/16.0))
}

// nearestPaletteColor returns the color space entry that is perceptually
// closest (CIEDE2000) to target. An empty color space yields black/code 0.
func nearestPaletteColor(colorSpace ColorSpace, target Colorf) (uint8, Colorf) {
	minDist := math.MaxFloat64
	bestColorCode := uint8(0x0)
	bestColor := Colorf{0, 0, 0}

	for _, def := range colorSpace {
		dist := ciede2000.Diff(convertToRGBA(target), convertToRGBA(def.Color))
		if dist < minDist {
			minDist = dist
			bestColorCode = def.Code
			bestColor = def.Color
		}
	}

	return bestColorCode, bestColor
}

func ConvertToEInkImage(src image.Image, colorSpace ColorSpace) []byte {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	image := ConvertImageToColors(src)

	var einkImage []byte

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// find closest color
			imageColor := CorrectGamma(image[y][x])
			bestColorCode, bestColor := nearestPaletteColor(colorSpace, imageColor)

			einkImage = append(einkImage, bestColorCode)

			// dither the remaining color using Floyd-Steinberg to create the illusion of shading
			diff := subtractColorf(imageColor, bestColor)
			ditherFloydSteinberg(image, x, y, diff)
		}
	}

	// each byte holds two pixels for eink display
	einkImage = packBytesIntoNibbles(einkImage)

	return einkImage
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// errEmptyColorSpace is returned when a request carries no color space; the
// callers cannot pick any color code without one.
var errEmptyColorSpace = errors.New("color space is empty")

func GenerateCalibrationImage(width, height int, colorSpace ColorSpace) ([]byte, error) {
	var einkImage []byte

	colorCount := len(colorSpace)
	if colorCount == 0 {
		return nil, errEmptyColorSpace
	}

	// calculate number of rows and columns (sideLength)
	sideLengthFloat := math.Sqrt(float64(colorCount))
	sideLength := int(sideLengthFloat)
	if float64(sideLength) != sideLengthFloat {
		sideLength = sideLength + 1
	}

	// A panel smaller than the grid gives a spacing of 0, which would divide by
	// zero below; one pixel per cell is the best that fits.
	rowSpacing := height / sideLength
	if rowSpacing < 1 {
		rowSpacing = 1
	}
	columnSpacing := width / sideLength
	if columnSpacing < 1 {
		columnSpacing = 1
	}

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			row := y / rowSpacing
			column := x / columnSpacing
			colorIndex := min(colorCount-1, row*sideLength+column)
			einkImage = append(einkImage, colorSpace[colorIndex].Code)
		}
	}

	return packBytesIntoNibbles(einkImage), nil
}

func GenerateClearImage(width, height int, colorSpace ColorSpace) ([]byte, error) {
	var einkImage []byte

	if len(colorSpace) == 0 {
		return nil, errEmptyColorSpace
	}

	// Clear to whichever entry of the panel's own palette is closest to white.
	// The index differs per panel (1 on the color panels, 15 on the 16-gray
	// one), so it has to come from the palette rather than a constant.
	clearCode, _ := nearestPaletteColor(colorSpace, Colorf{1, 1, 1})

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			einkImage = append(einkImage, clearCode)
		}
	}

	return packBytesIntoNibbles(einkImage), nil
}

func packBytesIntoNibbles(input []byte) []byte {
	var output []byte

	for x := 0; x < len(input); x += 2 {
		// An odd pixel count leaves a half-full byte; pad the low nibble.
		var low byte
		if x+1 < len(input) {
			low = input[x+1]
		}
		output = append(output, (input[x]<<4)|low)
	}

	return output
}
