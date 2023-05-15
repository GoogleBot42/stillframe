package main

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"

	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/mattn/go-ciede2000"

	"github.com/go-chi/chi/v5"

	"github.com/muesli/smartcrop"
	"github.com/muesli/smartcrop/nfnt"
	"github.com/nfnt/resize"
)

// Define a function to handle incoming requests
func requestHandler(w http.ResponseWriter, r *http.Request) {
	// Get the 'name' variable from the request
	name := chi.URLParam(r, "name")

	// Use a default value if 'name' is not present
	if name == "" {
		name = "World"
	}

	// Respond with a greeting message
	fmt.Fprintf(w, "Hello, %s!\n", name)
}

type EInkColor uint8

const (
	EPD_7IN3F_BLACK  EInkColor = 0x0
	EPD_7IN3F_WHITE  EInkColor = 0x1
	EPD_7IN3F_GREEN  EInkColor = 0x2
	EPD_7IN3F_BLUE   EInkColor = 0x3
	EPD_7IN3F_RED    EInkColor = 0x4
	EPD_7IN3F_YELLOW EInkColor = 0x5
	EPD_7IN3F_ORANGE EInkColor = 0x6
)

type ColorDefinition struct {
	Color Colorf
	Code  EInkColor
}

var Colors = []ColorDefinition{
	{Colorf{0, 0, 0}, EPD_7IN3F_BLACK},
	{Colorf{1, 1, 1}, EPD_7IN3F_WHITE},
	{Colorf{0.059, 0.329, 0.119}, EPD_7IN3F_GREEN},
	{Colorf{0.061, 0.147, 0.336}, EPD_7IN3F_BLUE},
	{Colorf{0.574, 0.066, 0.010}, EPD_7IN3F_RED},
	{Colorf{0.982, 0.756, 0.004}, EPD_7IN3F_YELLOW},
	{Colorf{0.795, 0.255, 0.018}, EPD_7IN3F_ORANGE},
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

// Colorf represents a color with float64 RGB values.
type Colorf struct {
	R, G, B float64
}

// Convert an image to a 2D array of Colorf.
func convertImageToColors(src image.Image) [][]Colorf {
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
				R: float64(r) / 0xffff,
				G: float64(g) / 0xffff,
				B: float64(b) / 0xffff,
			}

			colorfImg[y][x] = colorf
		}
	}

	return colorfImg
}

func convertToRGBA(colorf Colorf) color.RGBA {
	// Convert the float64 color values to uint8, and ignore the alpha channel.
	return color.RGBA{
		R: uint8(colorf.R * 255),
		G: uint8(colorf.G * 255),
		B: uint8(colorf.B * 255),
		A: 255,
	}
}

func subtractColorf(c1, c2 Colorf) Colorf {
	return Colorf{
		R: c1.R - c2.R,
		G: c1.G - c2.G,
		B: c1.B - c2.B,
	}
}

func addColorf(c1, c2 Colorf) Colorf {
	return Colorf{
		R: c1.R + c2.R,
		G: c1.G + c2.G,
		B: c1.B + c2.B,
	}
}

func multColor(c Colorf, m float64) Colorf {
	return Colorf{
		R: c.R * m,
		G: c.G * m,
		B: c.B * m,
	}
}

func clampColor(value Colorf, min, max float64) Colorf {
	return Colorf{
		R: clamp(value.R, min, max),
		G: clamp(value.G, min, max),
		B: clamp(value.B, min, max),
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

func ditherFloydSteinberg(image [][]Colorf, x int, y int, diff Colorf) {
	height := len(image)
	width := len(image[0])

	if x < 0 || y < 0 || x >= width || y >= height {
		return
	}

	image[y][x] = clampColor(addColorf(image[y][x], diff), 0, 1)
}

func ConvertToEInkImage(src image.Image) []byte {
	bounds := src.Bounds()
	width, height := bounds.Max.X, bounds.Max.Y

	image := convertImageToColors(src)

	var einkImage []byte

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// find closest color
			minDist := math.MaxFloat64
			bestColorCode := EInkColor(0)
			bestColor := Colorf{0, 0, 0}
			for _, def := range Colors {
				dist := ciede2000.Diff(convertToRGBA(image[y][x]), convertToRGBA(def.Color))
				if dist < minDist {
					minDist = dist
					bestColorCode = def.Code
					bestColor = def.Color
				}
			}

			einkImage = append(einkImage, byte(bestColorCode))

			// dither colors using Floyd-Steinberg to create the illusion of shading
			diff := subtractColorf(image[y][x], bestColor)

			ditherFloydSteinberg(image, x+1, y, multColor(diff, 7.0/16.0))
			ditherFloydSteinberg(image, x-1, y+1, multColor(diff, 3.0/16.0))
			ditherFloydSteinberg(image, x, y+1, multColor(diff, 5.0/16.0))
			ditherFloydSteinberg(image, x+1, y+1, multColor(diff, 1.0/16.0))
		}
	}

	einkImage = packBytesIntoNibbles(einkImage)

	return einkImage
}

func packBytesIntoNibbles(input []byte) []byte {
	// input must divisible by 2

	var output []byte

	for x := 0; x < len(input); x += 2 {
		output = append(output, (input[x]<<4)|input[x+1])
	}

	return output
}

func getImage(w http.ResponseWriter, r *http.Request) {
	// Open the file
	// file, err := os.Open("image.jpg")
	// file, err := os.Open("image-out.jpg")
	file, err := os.Open("dahlia-out.jpg")
	if err != nil {
		fmt.Println("Error: File could not be opened")
		os.Exit(1)
	}
	defer file.Close()

	img, _, _ := image.Decode(file)

	analyzer := smartcrop.NewAnalyzer(nfnt.NewDefaultResizer())
	topCrop, _ := analyzer.FindBestCrop(img, 800, 480)

	fmt.Printf("Top crop: %+v\n", topCrop)

	type SubImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	croppedImg := img.(SubImager).SubImage(topCrop)

	resizedImg := resize.Resize(800, 480, croppedImg, resize.Lanczos3)

	data := ConvertToEInkImage(resizedImg)

	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	fmt.Printf("Bytes to send: %+v\n", len(data))

	w.Write(data)
}

func basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "username" || password != "password" {
			http.Error(w, "Unauthorized.", 401)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	fmt.Println("Hello, Nix!")

	// Create a new Chi router
	router := chi.NewRouter()

	// Register the requestHandler function to handle requests at the root path
	// and a path with the 'name' parameter
	router.Get("/", requestHandler)
	router.Get("/{name}", requestHandler)

	router.Group(func(r chi.Router) {
		// r.Use(basicAuth)
		r.Get("/getImage", getImage)
	})

	// Start the HTTP server on port 8080 and log any errors
	log.Fatal(http.ListenAndServe("0.0.0.0:8080", router))
}
