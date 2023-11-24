package main

import (
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

func ConvertToEInkImage(src image.Image, colorSpace ColorSpace) []byte {
	bounds := src.Bounds()
	width, height := bounds.Max.X, bounds.Max.Y

	image := ConvertImageToColors(src)

	var einkImage []byte

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// find closest color
			imageColor := CorrectGamma(image[y][x])
			minDist := math.MaxFloat64
			bestColorCode := uint8(0x0)
			bestColor := Colorf{0, 0, 0}
			for _, def := range colorSpace {
				dist := ciede2000.Diff(convertToRGBA(imageColor), convertToRGBA(def.Color))
				if dist < minDist {
					minDist = dist
					bestColorCode = def.Code
					bestColor = def.Color
				}
			}

			einkImage = append(einkImage, byte(bestColorCode))

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

func GenerateCalibrationImage(width, height int, colorSpace ColorSpace) []byte {
	var einkImage []byte

	colorCount := len(colorSpace)

	// calculate number of rows and columns (sideLength)
	sideLengthFloat := math.Sqrt(float64(colorCount))
	sideLength := int(sideLengthFloat)
	if float64(sideLength) != sideLengthFloat {
		sideLength = sideLength + 1
	}

	rowSpacing := height / sideLength
	columnSpacing := width / sideLength

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			row := y / rowSpacing
			column := x / columnSpacing
			colorIndex := min(colorCount-1, row*sideLength+column)
			einkImage = append(einkImage, colorSpace[colorIndex].Code)
		}
	}

	return packBytesIntoNibbles(einkImage)
}

func GenerateClearImage(width, height int, colorSpace ColorSpace) []byte {
	var einkImage []byte

	// Assume the first color is white
	// What really matters is that the color is the same for the enitre image
	clearIndex := 3

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			einkImage = append(einkImage, colorSpace[clearIndex].Code)
		}
	}

	return packBytesIntoNibbles(einkImage)
}

func packBytesIntoNibbles(input []byte) []byte {
	// input length must divisible by 2

	var output []byte

	for x := 0; x < len(input); x += 2 {
		output = append(output, (input[x]<<4)|input[x+1])
	}

	return output
}
