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

func getRandomFile(dir string) (string, error) {
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return "", err
	}

	rand.Seed(time.Now().UnixNano())
	n := rand.Intn(len(files))

	for _, file := range files {
		if !file.IsDir() {
			if n == 0 {
				return filepath.Join(dir, file.Name()), nil
			}
			n--
		}
	}

	return "", os.ErrNotExist
}

type ImageProperties struct {
	Width         int        `json:"width"`
	Height        int        `json:"height"`
	FlipVertical  bool       `json:"flip_vertical"`
	FlipHorizonal bool       `json:"flip_horizonal"`
	ColorSpace    ColorSpace `json:"color_space"`
}

func calibrationImage(w http.ResponseWriter, r *http.Request) {
	var imageProps ImageProperties
	err := json.NewDecoder(r.Body).Decode(&imageProps)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	data := GenerateCalibrationImage(imageProps.Width, imageProps.Height, imageProps.ColorSpace)

	fmt.Printf("Bytes to send: %+v\n", len(data))

	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

func fetchImage(w http.ResponseWriter, r *http.Request) {
	var imageProps ImageProperties
	err := json.NewDecoder(r.Body).Decode(&imageProps)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Now you can access the fields of imageProps, e.g.:
	fmt.Printf("Received request for an image with width: %d, height: %d\n", imageProps.Width, imageProps.Height)
	for _, color := range imageProps.ColorSpace {
		fmt.Printf("Color code: %d, RGB: %v\n", color.Code, color.Color)
	}

	randomImgFile, _ := getRandomFile("img")
	fmt.Println("Serving random image file:", randomImgFile)

	img := ReadImage(randomImgFile)

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

func main() {
	fmt.Println("Starting server")

	// Create a new Chi router
	router := chi.NewRouter()

	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)

	// Register the requestHandler function to handle requests at the root path
	// and a path with the 'name' parameter
	router.Get("/", requestHandler)
	router.Get("/{name}", requestHandler)

	router.Group(func(r chi.Router) {
		// r.Use(basicAuth)
		r.Post("/fetchImage", fetchImage)
		r.Post("/calibrationImage", calibrationImage)
	})

	// Start the HTTP server on port 8080 and log any errors
	log.Fatal(http.ListenAndServe("0.0.0.0:8080", router))
}
