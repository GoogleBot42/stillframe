package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
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

// The endpoints are unauthenticated by design, so every bound the pipeline
// needs has to be a hard limit here rather than an assumption about who is
// calling.
//
// maxDimension bounds either side of the panel and maxPixels bounds their
// product; the per-side cap alone still admits 10000x10000, which is 100 Mpx —
// a 50 MB response body, ~350 MiB of heap in GenerateClearImage alone, and a
// couple of gigabytes once /fetchImage's intermediate buffers are counted.
//
// The largest shipped panel is the 16-gray IT8951 at 1872x1404 (2.6 Mpx); the
// 13.3" Spectra is 1600x1200. The headroom has to be picked against measured
// cost rather than against the response size, because the response is the
// cheapest thing a request allocates: /fetchImage also holds the source RGBA,
// a [][]Colorf at 24 bytes per pixel, the one-byte-per-pixel code buffer and
// the dither matcher's per-distinct-color cache. Measured at 4000x2000
// (8 Mpx), one conversion peaked at 278 MB for a gradient and 524 MB for a
// high-color photo — more than the 256 MiB the service VM is given. 4 Mpx is
// ~1.5x the biggest panel we ship, halves those numbers, and is one constant
// to raise the day a bigger panel exists.
const (
	maxDimension = 10000
	maxPixels    = 4_000_000
)

// maxColorSpaceEntries bounds the palette. A pixel is packed into a 4-bit
// nibble, so only 16 distinct codes exist and the 16-gray panel already uses
// every one of them; 64 leaves room for a palette that repeats codes while
// keeping the nearest-color search — which is linear in the palette and runs
// per distinct pixel color — from turning one request into minutes of CPU.
const maxColorSpaceEntries = 64

// maxColorCode is the largest value that survives the nibble packing. A larger
// code does not merely render wrong, it bleeds into the neighbouring pixel.
const maxColorCode = 0x0F

// maxBodyBytes caps the request body. The largest body the firmware sends is
// the 16-entry grey16 palette, under 1 KB; 16 KiB is ample for the 64 entries
// this server accepts and still small enough that an anonymous POST cannot
// make the server buffer anything worth noticing.
const maxBodyBytes = 16 << 10

// maxConcurrentRequests bounds how many image requests are converted at once.
// Even at maxPixels a single conversion costs a couple of hundred MB, so
// without a limit N frames waking on the same minute multiply the peak by N —
// the caps above bound one request, this bounds the server. The work is
// CPU-bound, so a small number costs no throughput: a household of frames
// serialises through it in a few seconds, well inside the firmware's HTTP
// timeout, and a client that gives up waiting frees its place without ever
// having allocated anything.
const maxConcurrentRequests = 2

// imageSlots is the semaphore limitConcurrency hands out. Buffered, so an
// acquire is a send and a release is a receive.
var imageSlots = make(chan struct{}, maxConcurrentRequests)

// limitConcurrency holds a request at the door until a conversion slot is
// free. Waiting is the right answer rather than shedding: a frame that gets a
// 503 shows a stale picture for a whole sleep cycle. A caller that disconnects
// or times out first cancels its context and is answered 503 instead of being
// converted for nobody.
func limitConcurrency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case imageSlots <- struct{}{}:
			defer func() { <-imageSlots }()
			next.ServeHTTP(w, r)
		case <-r.Context().Done():
			http.Error(w, "server busy", http.StatusServiceUnavailable)
		}
	})
}

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

// rejectBody answers a body that could not be read: over the byte cap is 413,
// anything else is a malformed 400.
func rejectBody(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, fmt.Sprintf("request body must not exceed %d bytes", maxBodyBytes), http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

// decodeImageProps decodes and validates a request body, answering 400 itself
// when either fails. The bodies come from the network, so every field that the
// image pipeline divides by or indexes with has to be checked here.
func decodeImageProps(w http.ResponseWriter, r *http.Request) (ImageProperties, bool) {
	var imageProps ImageProperties

	// Read under a hard cap rather than trusting the body to be the small JSON
	// the firmware sends: nothing authenticates the caller.
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&imageProps); err != nil {
		rejectBody(w, err)
		return imageProps, false
	}
	// json.Decoder stops at the end of the first value, so on its own the cap
	// above bounds what the decoder happened to read and not the body — a
	// valid object followed by a megabyte of padding would be served. Insist
	// the object is the whole body; the second read is what trips the cap.
	if err := dec.Decode(new(struct{})); err != io.EOF {
		if err == nil {
			err = errors.New("body must contain exactly one JSON object")
		}
		rejectBody(w, err)
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
	// Both sides are in [1, maxDimension] by now, so the product cannot
	// overflow an int on any platform Go supports.
	if imageProps.Width*imageProps.Height > maxPixels {
		http.Error(w, fmt.Sprintf("width*height must not exceed %d pixels", maxPixels), http.StatusBadRequest)
		return imageProps, false
	}
	if len(imageProps.ColorSpace) == 0 {
		http.Error(w, "color_space must not be empty", http.StatusBadRequest)
		return imageProps, false
	}
	if len(imageProps.ColorSpace) > maxColorSpaceEntries {
		http.Error(w, fmt.Sprintf("color_space must not have more than %d entries", maxColorSpaceEntries), http.StatusBadRequest)
		return imageProps, false
	}
	for i, entry := range imageProps.ColorSpace {
		if entry.Code > maxColorCode {
			http.Error(w, fmt.Sprintf("color_space[%d].color_code must be between 0 and %d", i, maxColorCode), http.StatusBadRequest)
			return imageProps, false
		}
		for c, channel := range entry.Color {
			// uint8(channel*255) in convertToRGBA is implementation-defined
			// outside [0, 1] — measured -5 -> 5, 300 -> 212, 1e9 -> 0 — so an
			// out-of-range channel matches pixels against a color nobody
			// asked for. Phrased as a negated in-range test so a NaN, which
			// compares false against everything, cannot slip past either;
			// encoding/json cannot produce one today, but the guard costs
			// nothing and does not depend on that staying true.
			if !(channel >= 0 && channel <= 1) {
				http.Error(w, fmt.Sprintf("color_space[%d].rgb_color[%d] must be between 0 and 1", i, c), http.StatusBadRequest)
				return imageProps, false
			}
		}
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

	// The client is gone; don't burn a conversion slot on a picture nobody will
	// read.
	if err := r.Context().Err(); err != nil {
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
		r.Use(limitConcurrency)
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
