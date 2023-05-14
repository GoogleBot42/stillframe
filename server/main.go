package main

import (
	"fmt"
	"log"
	"net/http"
	"strconv"

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

const (
	EPD_7IN3F_BLACK  = 0x0
	EPD_7IN3F_WHITE  = 0x1
	EPD_7IN3F_GREEN  = 0x2
	EPD_7IN3F_BLUE   = 0x3
	EPD_7IN3F_RED    = 0x4
	EPD_7IN3F_YELLOW = 0x5
	EPD_7IN3F_ORANGE = 0x6
)

func getImage(w http.ResponseWriter, r *http.Request) {
	colors := []byte{
		EPD_7IN3F_BLACK, EPD_7IN3F_BLUE, EPD_7IN3F_GREEN, EPD_7IN3F_ORANGE,
		EPD_7IN3F_RED, EPD_7IN3F_YELLOW, EPD_7IN3F_WHITE, EPD_7IN3F_WHITE,
	}

	var data []byte

	for i := 0; i < 240; i++ {
		for k := 0; k < 4; k++ {
			for j := 0; j < 100; j++ {
				data = append(data, (colors[k]<<4)|colors[k])
			}
		}
	}

	for i := 0; i < 240; i++ {
		for k := 4; k < 8; k++ {
			for j := 0; j < 100; j++ {
				data = append(data, (colors[k]<<4)|colors[k])
			}
		}
	}

	// // Fill the array with byte K
	// for i := 0; i < 800*480/2; i++ {
	// 	data = append(data, (colors[0]<<4)|colors[6])
	// }

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
