package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	// Immich's preview is whatever its admin configured the server to
	// transcode to: JPEG out of the box, WebP if the "thumbnail format"
	// setting was changed. The standard library has no WebP decoder, so
	// registering this one is what keeps that setting from turning every
	// refresh into a decode failure. It also lets a .webp file sitting in the
	// local image directory be served — which is why the registration lives
	// here, next to the standard-library decoders, rather than in immich.go:
	// source.go's decodableExtensions offers .webp before every extension-less
	// file, so a build without this import would prefer files it cannot decode.
	_ "golang.org/x/image/webp"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

var imageDir = "./img"
var port = "8080"

// imageSource is where /fetchImage gets its photo. main() replaces it once the
// positional arguments and the environment have been read; it is initialised
// here as well so that anything constructing the router without going through
// main() — the tests do — has a working source rather than a nil interface.
var imageSource ImageSource = &localDirSource{dir: imageDir}

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

// The http.Server timeouts. http.ListenAndServe applies none of these, which
// leaves a frame that drops off WiFi mid-request holding a goroutine (and its
// buffers) until the process restarts, and makes Slowloris a one-line attack
// on an endpoint that authenticates nobody.
//
// The write deadline is the one that has to be reasoned about rather than
// picked, because it bounds the whole handler, not just the socket write. The
// worst case a legitimate /fetchImage can take is, in order:
//
//   - the conversions ahead of it in limitConcurrency's queue (this deadline
//     is set before the handler runs, so queueing counts against it). The
//     queue has no depth cap, so this is the *normal* worst case — a handful
//     of frames waking in the same minute — not a proven bound;
//   - the Immich download, itself bounded by immichTimeout (30 s), plus the
//     local-directory fallback retrying past undecodable files if Immich
//     fails;
//   - the conversion: face detection, Lanczos3 resize, CIEDE2000 nearest-color
//     mapping and Floyd-Steinberg dithering of an image up to maxPixels
//     (4 Mpx). On a Pi-class CPU that is the tens-of-seconds part.
//
// 180 s is comfortably above the sum of those and still an order of magnitude
// below "forever". A frame that hits it gets a truncated body and retries on
// its next wake, which is exactly what it does for any other failed refresh.
//
// The read deadlines can be short because the request body is capped at
// maxBodyBytes (16 KiB): no honest client needs 15 s to send under a kilobyte
// of JSON, and ReadHeaderTimeout is what actually stops Slowloris. But the
// read deadline, like the write one, is armed when the request line arrives,
// so it too would be eaten by time spent in limitConcurrency's queue — which
// is why the body is decoded by withImageProps *before* the slot is taken,
// not by the handler after it. IdleTimeout bounds a kept-alive connection
// between requests; the firmware sends one request per wake and then sleeps,
// so nothing legitimate sits idle.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 180 * time.Second
	idleTimeout       = 60 * time.Second
)

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

// imagePropsKey is the request-context key withImageProps stores the decoded
// body under.
type imagePropsKey struct{}

// withImageProps decodes and validates the request body before the request
// reaches limitConcurrency, and hands the result to the handler through the
// request context. The order is the point: the body read is under the server's
// read deadline, which starts ticking when the request line arrives, so
// reading it *after* queueing for a conversion slot would turn a busy server
// into a 400 for whichever frame waited longest. Decoding first also means a
// client that trickles its body in never occupies one of the two slots, and a
// malformed request is refused without waiting for one at all.
func withImageProps(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		imageProps, ok := decodeImageProps(w, r)
		if !ok {
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), imagePropsKey{}, imageProps)))
	})
}

// requestImageProps returns the body withImageProps decoded. Only reachable
// behind that middleware — newRouter wires it — so a missing value is a
// programming error, not a client one, and the panic is the right answer.
func requestImageProps(r *http.Request) ImageProperties {
	return r.Context().Value(imagePropsKey{}).(ImageProperties)
}

// writeImage answers with the nibble-packed panel bytes.
//
// The Content-Type is not a formality. Without one Go sniffs the body, and a
// packed image is just bytes: a bright 16-gray frame measurably sniffs as
// text/plain; charset=utf-8, which is an invitation for any reverse proxy in
// front of this server to transcode, recompress or otherwise rewrite what the
// firmware then clocks straight out to the panel. Declaring the body opaque is
// the whole fix.
func writeImage(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

func calibrationImage(w http.ResponseWriter, r *http.Request) {
	imageProps := requestImageProps(r)

	data, err := GenerateCalibrationImage(imageProps.Width, imageProps.Height, imageProps.ColorSpace)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("calibrationImage: %dx%d, %d colors, %d bytes",
		imageProps.Width, imageProps.Height, len(imageProps.ColorSpace), len(data))

	writeImage(w, data)
}

func clearImage(w http.ResponseWriter, r *http.Request) {
	imageProps := requestImageProps(r)

	data, err := GenerateClearImage(imageProps.Width, imageProps.Height, imageProps.ColorSpace)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("clearImage: %dx%d, %d colors, %d bytes",
		imageProps.Width, imageProps.Height, len(imageProps.ColorSpace), len(data))

	writeImage(w, data)
}

func fetchImage(w http.ResponseWriter, r *http.Request) {
	imageProps := requestImageProps(r)

	// The client is gone; don't burn a conversion slot on a picture nobody will
	// read.
	if err := r.Context().Err(); err != nil {
		return
	}

	img, randomImgFile, err := imageSource.NextImage(r.Context())
	if err != nil {
		// With Immich configured this is the error from the *fallback*: the
		// Immich failure has already been logged by fallbackSource, so both
		// halves of "neither source could produce a picture" are in the log.
		log.Printf("picking an image: %v", err)
		http.Error(w, "no image available", http.StatusInternalServerError)
		return
	}

	flippedImage := flipImage(img, imageProps.FlipVertical, imageProps.FlipHorizonal)
	bestCrop := GetBestPieceOfImage(imageProps.Width, imageProps.Height, flippedImage)
	data := ConvertToEInkImage(bestCrop, imageProps.ColorSpace)

	// One line per request, not one per palette entry: a frame that wakes every
	// few minutes with a 16-entry palette used to write seventeen lines into
	// journald for a refresh nobody was watching.
	log.Printf("fetchImage: %dx%d, %d colors, %q, %d bytes",
		imageProps.Width, imageProps.Height, len(imageProps.ColorSpace), randomImgFile, len(data))

	writeImage(w, data)
}

// newRouter builds the server's routing table. main() and the tests both use it
// so that route or middleware drift is caught by the tests.
func newRouter() *chi.Mux {
	router := chi.NewRouter()

	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)

	// The endpoints are deliberately unauthenticated; enforcing auth is the
	// reverse proxy's job (see README.md). TestRoutesAreUnauthenticated pins
	// that, so wiring a middleware in here fails a test rather than silently
	// changing the contract the firmware was built against.
	router.Group(func(r chi.Router) {
		// Body first, slot second — see withImageProps for why the order
		// matters. TestBodyIsDecodedBeforeQueueing pins it.
		r.Use(withImageProps)
		r.Use(limitConcurrency)
		r.Post("/fetchImage", fetchImage)
		r.Post("/calibrationImage", calibrationImage)
		r.Post("/clearImage", clearImage)
	})

	return router
}

// parseArgs reads the command line — everything after the program name.
//
// The bind address is a flag rather than a third positional argument because
// `server <port> [imgDir]` is the form server/service.nix interpolates and
// every deployed unit already runs; a NixOS module people have deployed is not
// something to break for an option most installs will leave alone. Go's flag
// package stops parsing at the first non-flag argument, so both
// `server 8080 /imgs` and `server -bind 127.0.0.1 8080 /imgs` mean what they
// look like. (Anything flag-shaped *after* the port is therefore a positional,
// not a flag — fine while -bind is the only one, and the reason a second flag
// would need this comment revisited.)
//
// Defaults are passed in rather than read from the package variables so that a
// test can call this without disturbing them.
func parseArgs(args []string, defaultPort, defaultImgDir string) (bind, port, imgDir string, err error) {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	bindFlag := fs.String("bind", "0.0.0.0", "address to listen on (0.0.0.0 for every interface, 127.0.0.1 for loopback only, or a specific address such as a Tailscale one)")
	if err := fs.Parse(args); err != nil {
		return "", "", "", err
	}

	port, imgDir = defaultPort, defaultImgDir
	rest := fs.Args()
	if len(rest) > 0 {
		port = rest[0]
	}
	if len(rest) > 1 {
		imgDir = rest[1]
	}
	return *bindFlag, port, imgDir, nil
}

func main() {
	bind, parsedPort, parsedImgDir, err := parseArgs(os.Args[1:], port, imageDir)
	if err != nil {
		// flag.ContinueOnError has already printed the usage, and the error
		// too unless the "error" was someone asking for -h.
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		os.Exit(2)
	}
	port, imageDir = parsedPort, parsedImgDir
	log.Printf("serving images from %q", imageDir)

	// The image source is chosen from the environment rather than from more
	// positional arguments: server/service.nix interpolates the port and the
	// directory by position, and a NixOS module that has been deployed is not
	// something to break for a feature most installs will not turn on.
	local := &localDirSource{dir: imageDir}
	imageSource = local
	if immich, err := immichSourceFromEnv(); err != nil {
		// Half-configured is a mistake worth naming, but not worth refusing to
		// start over: the frames still get pictures from the directory.
		log.Printf("warning: ignoring the Immich configuration (%v); serving only from %q", err, imageDir)
	} else if immich != nil {
		imageSource = fallbackSource{primary: immich, fallback: local}
		log.Printf("image sources: Immich at %s (%s), falling back to %q", immich.baseURL, immich.albumDescription(), imageDir)
	}

	addr := net.JoinHostPort(bind, port)
	server := &http.Server{
		Addr:              addr,
		Handler:           newRouter(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	log.Printf("listening on %s", addr)
	log.Fatal(server.ListenAndServe())
}
