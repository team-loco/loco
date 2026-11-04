package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

//go:embed gen/openapi/openapi.json
var openAPISchema []byte

//go:embed docs.html
var openAPIDocs []byte

func mountOpenAPI(mux *http.ServeMux, buildVersion string) {
	schema := versionedOpenAPI(buildVersion)
	for _, file := range []struct {
		path        string
		contentType string
		body        []byte
	}{
		{"/openapi.json", "application/json", schema},
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

func versionedOpenAPI(buildVersion string) []byte {
	reader := bytes.NewReader(openAPISchema)
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		panic(fmt.Errorf("decode embedded OpenAPI schema: %w", err))
	}
	info, ok := document["info"].(map[string]any)
	if !ok {
		panic("embedded OpenAPI schema must include an info object")
	}
	info["version"] = buildVersion
	schema, err := json.Marshal(document)
	if err != nil {
		panic(fmt.Errorf("encode versioned OpenAPI schema: %w", err))
	}
	return schema
}
