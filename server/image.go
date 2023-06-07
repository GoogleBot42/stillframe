package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
)

func ReadImage(name string) image.Image {
	file, err := os.Open(name)
	if err != nil {
		fmt.Println("Error: File could not be opened")
		os.Exit(1)
	}
	defer file.Close()

	img, _, _ := image.Decode(file)

	return img
}

func imageToRGBA(src image.Image) *image.RGBA {
	// No conversion needed if image is an *image.RGBA.
	if dst, ok := src.(*image.RGBA); ok {
		return dst
	}

	// Use the image/draw package to convert to *image.RGBA.
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

// Colorf represents a color with float64 RGB values. [R, G, B]
type Colorf [3]float64

// Convert an image to a 2D array of Colorf.
func ConvertImageToColors(src image.Image) [][]Colorf {
	img := imageToRGBA(src)

	bounds := img.Bounds()
	width, height := bounds.Max.X, bounds.Max.Y

	// Initialize the 2D slice.
	colorfImg := make([][]Colorf, height)
	for i := range colorfImg {
		colorfImg[i] = make([]Colorf, width)
	}

	// Iterate over each pixel.
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r, g, b, _ := img.At(x, y).RGBA()

			// Convert the uint32 color values to float64, normalized to [0, 1].
			colorf := Colorf{
				float64(r) / 0xffff,
				float64(g) / 0xffff,
				float64(b) / 0xffff,
			}

			colorfImg[y][x] = colorf
		}
	}

	return colorfImg
}

func CorrectGamma(in Colorf) Colorf {
	gamma := func(in float64) float64 {
		return math.Pow(in, 1.2)

		// This is the correct algorithm but looks too dark
		// if in > 0.04045 {
		// 	return math.Pow((in+0.055)/(1.0+0.055), 2.4)
		// } else {
		// 	return in / 12.92
		// }
	}

	return Colorf{
		gamma(in[0]),
		gamma(in[1]),
		gamma(in[2]),
	}
}

func convertToRGBA(colorf Colorf) color.RGBA {
	// Convert the float64 color values to uint8, and ignore the alpha channel.
	return color.RGBA{
		R: uint8(colorf[0] * 255),
		G: uint8(colorf[1] * 255),
		B: uint8(colorf[2] * 255),
		A: 255,
	}
}

func subtractColorf(c1, c2 Colorf) Colorf {
	return Colorf{
		c1[0] - c2[0],
		c1[1] - c2[1],
		c1[2] - c2[2],
	}
}

func addColorf(c1, c2 Colorf) Colorf {
	return Colorf{
		c1[0] + c2[0],
		c1[1] + c2[1],
		c1[2] + c2[2],
	}
}

func multColor(c Colorf, m float64) Colorf {
	return Colorf{
		c[0] * m,
		c[1] * m,
		c[2] * m,
	}
}

func clampColor(value Colorf, min, max float64) Colorf {
	return Colorf{
		clamp(value[0], min, max),
		clamp(value[1], min, max),
		clamp(value[2], min, max),
	}
}

func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func flipImage(img image.Image, vertical, horizontal bool) image.Image {
	bounds := img.Bounds()
	width, height := bounds.Max.X, bounds.Max.Y

	newImg := image.NewRGBA(image.Rect(0, 0, width, height))

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			srcX, srcY := x, y

			if horizontal {
				srcX = width - x - 1
			}

			if vertical {
				srcY = height - y - 1
			}

			c := img.At(srcX, srcY)
			newImg.Set(x, y, c)
		}
	}

	return newImg
}
