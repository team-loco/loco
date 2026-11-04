package main

import (
	"bytes"
	_ "embed"
	"net/http"
	"time"
)

//go:embed gen/openapi/openapi.json
var openAPISchema []byte

//go:embed docs.html
var openAPIDocs []byte

func mountOpenAPI(mux *http.ServeMux) {
	for _, file := range []struct {
		path        string
		contentType string
		body        []byte
	}{
		{"/openapi.json", "application/json", openAPISchema},
		{rootPath + "{$}", "text/html; charset=utf-8", openAPIDocs},
	} {
		mux.HandleFunc(file.path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", file.contentType)
			reader := bytes.NewReader(file.body)
			http.ServeContent(w, r, file.path, time.Time{}, reader)
		})
	}
}
