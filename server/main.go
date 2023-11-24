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

func clearImage(w http.ResponseWriter, r *http.Request) {
	var imageProps ImageProperties
	err := json.NewDecoder(r.Body).Decode(&imageProps)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	data := GenerateClearImage(imageProps.Width, imageProps.Height, imageProps.ColorSpace)

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

	randomImgFile, _ := getRandomFile(imageDir)
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
	if len(os.Args) > 1 {
		port = os.Args[1]
	}
	fmt.Println("Starting on port: ", port)

	if len(os.Args) > 2 {
		imageDir = os.Args[2]
	}
	fmt.Println("Choosing images from: ", imageDir)

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

	fmt.Println("Started server")

	// Start the HTTP server
	log.Fatal(http.ListenAndServe("0.0.0.0:"+port, router))
}
