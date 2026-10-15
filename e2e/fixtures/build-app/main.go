package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	message := os.Getenv("MESSAGE")
	if message == "" {
		message = "built by loco"
	}
	server := &http.Server{Addr: ":8080", Handler: newHandler(message), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}

func newHandler(message string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, message)
	})
	return mux
}
