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
	"image/jpeg"
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
	Color color.RGBA
	Code  EInkColor
}

// var Colors = []ColorDefinition{
// 	{color.RGBA{0, 0, 0, 255}, EPD_7IN3F_BLACK},
// 	{color.RGBA{255, 255, 255, 255}, EPD_7IN3F_WHITE},
// 	{color.RGBA{0, 255, 0, 255}, EPD_7IN3F_GREEN},
// 	{color.RGBA{0, 0, 255, 255}, EPD_7IN3F_BLUE},
// 	{color.RGBA{255, 0, 0, 255}, EPD_7IN3F_RED},
// 	{color.RGBA{255, 255, 0, 255}, EPD_7IN3F_YELLOW},
// 	{color.RGBA{255, 165, 0, 255}, EPD_7IN3F_ORANGE},
// }

var Colors = []ColorDefinition{
	{color.RGBA{0, 0, 0, 255}, EPD_7IN3F_BLACK},
	{color.RGBA{255, 255, 255, 255}, EPD_7IN3F_WHITE},
	{color.RGBA{16, 84, 31, 255}, EPD_7IN3F_GREEN},
	{color.RGBA{16, 38, 86, 255}, EPD_7IN3F_BLUE},
	{color.RGBA{147, 17, 3, 255}, EPD_7IN3F_RED},
	{color.RGBA{251, 193, 2, 255}, EPD_7IN3F_YELLOW},
	{color.RGBA{203, 66, 5, 255}, EPD_7IN3F_ORANGE},
}

func ConvertToEInkColor(c color.RGBA) EInkColor {
	minDist := math.MaxFloat64
	minCode := EInkColor(0)
	for _, def := range Colors {
		dist := colorDistance(c, def.Color)
		if dist < minDist {
			minDist = dist
			minCode = def.Code
		}
	}
	return minCode
}

func colorDistance(a, b color.RGBA) float64 {
	// r := float64(a.R) - float64(b.R)
	// g := float64(a.G) - float64(b.G)
	// bb := float64(a.B) - float64(b.B)
	// return math.Sqrt(r*r + g*g + bb*bb)
	return ciede2000.Diff(a, b)
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

func ConvertToEInkImage(src image.Image) []byte {
	rgbImage := imageToRGBA(src)

	var image []byte

	for y := 0; y < src.Bounds().Max.Y; y++ {
		for x := 0; x < src.Bounds().Max.X; x++ {
			image = append(image, byte(ConvertToEInkColor(rgbImage.RGBAAt(x, y))))
		}
	}

	image = packBytesIntoNibbles(image)

	return image
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

	// Now you can save the cropped image
	outFile, err := os.Create("cropped_image.jpg")
	if err != nil {
		fmt.Println("Error: File could not be created")
		os.Exit(1)
	}
	defer outFile.Close()

	err = jpeg.Encode(outFile, resizedImg, nil)
	if err != nil {
		fmt.Println("Error: Image could not be encoded")
		os.Exit(1)
	}

	fmt.Println("Cropped image saved successfully")

	// data := ConvertToEInkImage(resizedImg)

	colors := []EInkColor{
		EPD_7IN3F_BLACK, EPD_7IN3F_BLUE, EPD_7IN3F_GREEN, EPD_7IN3F_ORANGE,
		EPD_7IN3F_RED, EPD_7IN3F_YELLOW, EPD_7IN3F_WHITE, EPD_7IN3F_WHITE,
	}

	var data []byte

	for i := 0; i < 240; i++ {
		for k := 0; k < 4; k++ {
			for j := 0; j < 100; j++ {
				data = append(data, (byte(colors[k]<<4))|byte(colors[k]))
			}
		}
	}

	for i := 0; i < 240; i++ {
		for k := 4; k < 8; k++ {
			for j := 0; j < 100; j++ {
				data = append(data, (byte(colors[k]<<4))|byte(colors[k]))
			}
		}
	}

	// // Fill the array with byte K
	// for i := 0; i < 800*480/2; i++ {
	// 	data = append(data, (colors[0]<<4)|colors[6])
	// }

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
