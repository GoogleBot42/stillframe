package main

import (
	"encoding/json"
	"fmt"
	"image"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

// decodableExtensions are the file suffixes image.Decode can handle with the
// decoders this package registers. A .heic exported from a phone, a .mp4 or a
// stray .txt is a guaranteed decode failure, so it is not drawn while anything
// better is available.
var decodableExtensions = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".jpe":  true,
	".jfif": true,
	".gif":  true,
}

// The draw has no memory of its own, so a small directory shows the same photo
// twice in a row surprisingly often (1 in 20 for 20 images). Remembering the
// file the last request served, and trying it last, removes the back-to-back
// repeat. It is one process-wide value, so with several frames pointed at one
// server the guarantee weakens to "not the last picture this server served".
// Guarded by a mutex: chi serves every request in its own goroutine.
var (
	lastServedMu   sync.Mutex
	lastServedFile string
)

func rememberServed(path string) {
	lastServedMu.Lock()
	lastServedFile = path
	lastServedMu.Unlock()
}

func lastServed() string {
	lastServedMu.Lock()
	defer lastServedMu.Unlock()
	return lastServedFile
}

// imageCandidates lists the files in dir worth trying, in the order to try
// them. Sub-directories are not pictures, and dotfiles are either metadata
// (.DS_Store) or rsync's partial transfers, so neither is a candidate at all.
// Everything else is: the files whose extension the registered decoders
// understand come first, and the rest follow rather than being dropped, because
// image.Decode goes by content — an extension-less export is still a picture,
// and it must not be unreachable just because one .jpg in the directory happens
// to be corrupt.
func imageCandidates(dir string) ([]string, error) {
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var decodable, rest []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		if decodableExtensions[strings.ToLower(filepath.Ext(name))] {
			decodable = append(decodable, path)
		} else {
			rest = append(rest, path)
		}
	}

	last := lastServed()
	files := append(drawOrder(decodable, last), drawOrder(rest, last)...)
	if len(files) == 0 {
		return nil, fmt.Errorf("no usable image files in %q: %w", dir, os.ErrNotExist)
	}

	return files, nil
}

// drawOrder shuffles files and moves last, the previously served file, to the
// back — to the back rather than out of the list, so that a one-image directory
// keeps serving its one image and a directory whose other files are all broken
// still falls back to the one that works.
func drawOrder(files []string, last string) []string {
	rand.Shuffle(len(files), func(i, j int) { files[i], files[j] = files[j], files[i] })

	if last == "" || len(files) < 2 {
		return files
	}
	for i, f := range files {
		if f == last {
			copy(files[i:], files[i+1:])
			files[len(files)-1] = last
			break
		}
	}
	return files
}

// pickImage returns a decoded image from dir, walking past the candidates that
// fail to decode. A file can be undecodable despite its name (truncated,
// half-written mid-rsync, misnamed), and a single such file used to cost the
// frame a whole sleep cycle — up to hours of stale picture — because the
// request 500'd instead of trying another file. Only a directory where nothing
// at all decodes is an error; the walk is bounded by the candidate count, and
// each rejection costs one open plus a failed format sniff.
func pickImage(dir string) (image.Image, string, error) {
	candidates, err := imageCandidates(dir)
	if err != nil {
		return nil, "", err
	}

	var lastErr error
	for _, path := range candidates {
		img, err := ReadImage(path)
		if err == nil {
			rememberServed(path)
			return img, path, nil
		}
		log.Printf("skipping unusable image %q: %v", path, err)
		lastErr = err
	}

	// candidates is never empty, so a decode error is always set here.
	return nil, "", lastErr
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

	img, randomImgFile, err := pickImage(imageDir)
	if err != nil {
		log.Printf("picking an image from %q: %v", imageDir, err)
		http.Error(w, "no image available", http.StatusInternalServerError)
		return
	}
	fmt.Println("Serving random image file:", randomImgFile)

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
