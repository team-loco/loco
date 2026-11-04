package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
)

func TestOpenAPIDocs(t *testing.T) {
	mux := http.NewServeMux()
	mountOpenAPI(mux)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, rootPath, nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	body := response.Body.String()
	for _, want := range []string{
		"<title>Loco API</title>",
		`id="api-docs"`,
		`Scalar.createApiReference("#api-docs", {`,
		`url: "/openapi.json"`,
		`proxyUrl: ""`,
		"withDefaultFonts: false",
		"telemetry: false",
		"agent: { disabled: true }",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("docs missing %q", want)
		}
	}
	assetPattern := regexp.MustCompile(
		`https://cdn\.jsdelivr\.net/npm/@scalar/api-reference@\d+\.\d+\.\d+/dist/browser/standalone\.js`,
	)
	assets := assetPattern.FindAllStringSubmatch(body, -1)
	if len(assets) != 1 {
		t.Fatalf("expected one pinned Scalar CDN script, got %v", assets)
	}
	for _, method := range []string{http.MethodHead, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), method, rootPath, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if method == http.MethodHead {
				if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
					t.Errorf("HEAD: status %d, body length %d", rec.Code, rec.Body.Len())
				}
				return
			}
			if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
				t.Errorf("POST: status %d, Allow %q", rec.Code, rec.Header().Get("Allow"))
			}
		})
	}
}

func TestOpenAPIDocsUnknownPaths(t *testing.T) {
	mux := http.NewServeMux()
	mountOpenAPI(mux)
	for _, path := range []string{"/docs", "/docs/", "/missing"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", response.Code)
			}
		})
	}
}

func TestOpenAPI(t *testing.T) {
	mux := http.NewServeMux()
	mountOpenAPI(mux)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/openapi.json", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	var spec struct {
		OpenAPI string                     `json:"openapi"`
		Paths   map[string]json.RawMessage `json:"paths"`
		Info    struct {
			Title string `json:"title"`
		} `json:"info"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if spec.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI = %q", spec.OpenAPI)
	}
	if spec.Info.Title != "Loco API" {
		t.Errorf("title = %q, want Loco API", spec.Info.Title)
	}
	for _, path := range []string{
		configv1connect.ConfigServiceGetConfigProcedure,
		agentv1connect.AgentServiceSyncProcedure,
		observabilityv1connect.ObservabilityProxyServiceQueryLogsProcedure,
	} {
		if _, ok := spec.Paths[path]; !ok {
			t.Errorf("missing RPC path %s", path)
		}
	}
	for _, method := range []string{http.MethodHead, http.MethodPost} {
		req := httptest.NewRequestWithContext(t.Context(), method, "/openapi.json", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if method == http.MethodHead {
			if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
				t.Errorf("HEAD: status %d, body length %d", rec.Code, rec.Body.Len())
			}
		} else if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST: status = %d", rec.Code)
		}
	}
}
