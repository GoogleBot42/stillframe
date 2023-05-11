package main

import (
	"fmt"
	"log"
	"net/http"

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

func main() {
	fmt.Println("Hello, Nix!")

	// Create a new Chi router
	router := chi.NewRouter()

	// Register the requestHandler function to handle requests at the root path
	// and a path with the 'name' parameter
	router.Get("/", requestHandler)
	router.Get("/{name}", requestHandler)

	// Start the HTTP server on port 8080 and log any errors
	log.Fatal(http.ListenAndServe(":8080", router))
}
