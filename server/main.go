package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

var imageDir = "./img"
var port = "8080"

// maxDimension bounds the panel size a client may ask for. The largest shipped
// panel is 1872x1404; anything far beyond that is a malformed or hostile body.
const maxDimension = 10000

func getRandomFile(dir string) (string, error) {
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		return "", err
	}

	// Draw only from the files: counting sub-directories would make the draw
	// fall off the end of the list.
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}

	if len(files) == 0 {
		return "", fmt.Errorf("no files in %q: %w", dir, os.ErrNotExist)
	}

	return files[rand.Intn(len(files))], nil
}

type ImageProperties struct {
	Width         int        `json:"width"`
	Height        int        `json:"height"`
	FlipVertical  bool       `json:"flip_vertical"`
	FlipHorizonal bool       `json:"flip_horizonal"`
	ColorSpace    ColorSpace `json:"color_space"`
}

// decodeImageProps decodes and validates a request body, answering 400 itself
// when either fails. The bodies come from the network, so every field that the
// image pipeline divides by or indexes with has to be checked here.
func decodeImageProps(w http.ResponseWriter, r *http.Request) (ImageProperties, bool) {
	var imageProps ImageProperties
	if err := json.NewDecoder(r.Body).Decode(&imageProps); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return imageProps, false
	}

	if imageProps.Width < 1 || imageProps.Width > maxDimension {
		http.Error(w, fmt.Sprintf("width must be between 1 and %d", maxDimension), http.StatusBadRequest)
		return imageProps, false
	}
	if imageProps.Height < 1 || imageProps.Height > maxDimension {
		http.Error(w, fmt.Sprintf("height must be between 1 and %d", maxDimension), http.StatusBadRequest)
		return imageProps, false
	}
	if len(imageProps.ColorSpace) == 0 {
		http.Error(w, "color_space must not be empty", http.StatusBadRequest)
		return imageProps, false
	}

	return imageProps, true
}

func calibrationImage(w http.ResponseWriter, r *http.Request) {
	imageProps, ok := decodeImageProps(w, r)
	if !ok {
		return
	}

	data, err := GenerateCalibrationImage(imageProps.Width, imageProps.Height, imageProps.ColorSpace)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	fmt.Printf("Bytes to send: %+v\n", len(data))

	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

func clearImage(w http.ResponseWriter, r *http.Request) {
	imageProps, ok := decodeImageProps(w, r)
	if !ok {
		return
	}

	data, err := GenerateClearImage(imageProps.Width, imageProps.Height, imageProps.ColorSpace)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	fmt.Printf("Bytes to send: %+v\n", len(data))

	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

func fetchImage(w http.ResponseWriter, r *http.Request) {
	imageProps, ok := decodeImageProps(w, r)
	if !ok {
		return
	}

	// Now you can access the fields of imageProps, e.g.:
	fmt.Printf("Received request for an image with width: %d, height: %d\n", imageProps.Width, imageProps.Height)
	for _, color := range imageProps.ColorSpace {
		fmt.Printf("Color code: %d, RGB: %v\n", color.Code, color.Color)
	}

	randomImgFile, err := getRandomFile(imageDir)
	if err != nil {
		log.Printf("picking an image from %q: %v", imageDir, err)
		http.Error(w, "no image available", http.StatusInternalServerError)
		return
	}
	fmt.Println("Serving random image file:", randomImgFile)

	img, err := ReadImage(randomImgFile)
	if err != nil {
		log.Printf("reading image: %v", err)
		http.Error(w, "the selected image could not be read", http.StatusInternalServerError)
		return
	}

	flippedImage := flipImage(img, imageProps.FlipVertical, imageProps.FlipHorizonal)
	bestCrop := GetBestPieceOfImage(imageProps.Width, imageProps.Height, flippedImage)
	data := ConvertToEInkImage(bestCrop, imageProps.ColorSpace)

	fmt.Printf("Bytes to send: %+v\n", len(data))

	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
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

// newRouter builds the server's routing table. main() and the tests both use it
// so that route or middleware drift is caught by the tests.
func newRouter() *chi.Mux {
	router := chi.NewRouter()

	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)

	router.Group(func(r chi.Router) {
		// r.Use(basicAuth)
		r.Post("/fetchImage", fetchImage)
		r.Post("/calibrationImage", calibrationImage)
		r.Post("/clearImage", clearImage)
	})

	return router
}

func main() {
	if len(os.Args) > 1 {
		port = os.Args[1]
	}
	fmt.Println("Starting on port: ", port)

	if len(os.Args) > 2 {
		imageDir = os.Args[2]
	}
	fmt.Println("Choosing images from: ", imageDir)

	// Seed once at startup rather than on every draw.
	rand.Seed(time.Now().UnixNano())

	router := newRouter()

	fmt.Println("Started server")

	// Start the HTTP server
	log.Fatal(http.ListenAndServe("0.0.0.0:"+port, router))
}
