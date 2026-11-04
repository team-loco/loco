package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
)

func TestOpenAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(rootPath, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
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
	}
	if err := json.Unmarshal(response.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if spec.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI = %q", spec.OpenAPI)
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
