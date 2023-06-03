package main

import (
	"fmt"
	"log"
	"net/http"
	"strconv"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/go-chi/chi/v5"
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

var colorSpace = ColorSpace{
	{Colorf{0, 0, 0}, 0x0},
	{Colorf{1, 1, 1}, 0x1},
	{Colorf{0.059, 0.329, 0.119}, 0x2},
	{Colorf{0.061, 0.147, 0.336}, 0x3},
	{Colorf{0.574, 0.066, 0.010}, 0x4},
	{Colorf{0.982, 0.756, 0.004}, 0x5},
	{Colorf{0.795, 0.255, 0.018}, 0x6},
}

func getImage(w http.ResponseWriter, r *http.Request) {
	// img := ReadImage("image.jpg")
	// img := ReadImage("image-out.jpg")
	img := ReadImage("dahlia-out.jpg")
	bestCrop := GetBestPieceOfImage(800, 480, img)
	data := ConvertToEInkImage(bestCrop, colorSpace)

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
