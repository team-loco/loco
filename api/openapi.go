package main

import (
	"bytes"
	_ "embed"
	"net/http"
	"time"
)

//go:embed gen/openapi/openapi.json
var openAPISchema []byte

func mountOpenAPI(mux *http.ServeMux) {
	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		reader := bytes.NewReader(openAPISchema)
		http.ServeContent(w, r, "openapi.json", time.Time{}, reader)
	})
}
