package main

import (
	"image"
	"math"

	"github.com/mattn/go-ciede2000"
)

// Maps RGB colors to the eink display's color values
type ColorSpace []struct {
	Color Colorf
	Code  uint8
}

func ditherPixel(image [][]Colorf, x int, y int, diff Colorf) {
	height := len(image)
	width := len(image[0])

	if x < 0 || y < 0 || x >= width || y >= height {
		return
	}

	image[y][x] = clampColor(addColorf(image[y][x], diff), 0, 1)
}

func ditherFloydSteinberg(image [][]Colorf, x int, y int, color Colorf) {
	ditherPixel(image, x+1, y, multColor(color, 7.0/16.0))
	ditherPixel(image, x-1, y+1, multColor(color, 3.0/16.0))
	ditherPixel(image, x, y+1, multColor(color, 5.0/16.0))
	ditherPixel(image, x+1, y+1, multColor(color, 1.0/16.0))
}

func ConvertToEInkImage(src image.Image, colorSpace ColorSpace) []byte {
	bounds := src.Bounds()
	width, height := bounds.Max.X, bounds.Max.Y

	image := convertImageToColors(src)

	var einkImage []byte

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// find closest color
			minDist := math.MaxFloat64
			bestColorCode := uint8(0x0)
			bestColor := Colorf{0, 0, 0}
			for _, def := range colorSpace {
				dist := ciede2000.Diff(convertToRGBA(image[y][x]), convertToRGBA(def.Color))
				if dist < minDist {
					minDist = dist
					bestColorCode = def.Code
					bestColor = def.Color
				}
			}

			einkImage = append(einkImage, byte(bestColorCode))

			// dither the remaining color using Floyd-Steinberg to create the illusion of shading
			diff := subtractColorf(image[y][x], bestColor)
			ditherFloydSteinberg(image, x, y, diff)
		}
	}

	// each byte holds two pixels for eink display
	einkImage = packBytesIntoNibbles(einkImage)

	return einkImage
}

func packBytesIntoNibbles(input []byte) []byte {
	// input length must divisible by 2

	var output []byte

	for x := 0; x < len(input); x += 2 {
		output = append(output, (input[x]<<4)|input[x+1])
	}

	return output
}
