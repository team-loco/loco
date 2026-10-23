package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/team-loco/loco/observability-proxy/pkg/config"
)

const corsTestOrigin = "https://dashboard.example.test"

func TestProxyCORS(t *testing.T) {
	for _, test := range []struct {
		name    string
		origins []string
		origin  string
		allowed bool
	}{
		{name: "configured", origins: []string{corsTestOrigin}, origin: corsTestOrigin, allowed: true},
		{name: "unconfigured", origins: []string{corsTestOrigin}, origin: "https://other.example.test"},
		{name: "empty allowlist", origin: corsTestOrigin},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.Config{
				QueryEnabled:          true,
				ClickHouseURL:         unreachableMigrator,
				ClickHouseMigratorURL: unreachableMigrator,
				ClickHouseDB:          proxyDatabase,
				ControlPlaneURL:       unreachableControlPlane,
				TokenCacheTTL:         testTokenTTL,
				MaxConcurrent:         testMaxConcurrent,
				CORSAllowedOrigins:    test.origins,
			}
			proxy, err := newProxy(cfg, unlocked{}, testVersion)
			if err != nil {
				t.Fatalf("proxy: %v", err)
			}
			t.Cleanup(proxy.close)
			path := "/loco.observability.v1.ObservabilityProxyService/QueryLogs"
			request := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, path, nil)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Access-Control-Request-Method", http.MethodPost)
			request.Header.Set(
				"Access-Control-Request-Headers",
				"authorization,connect-protocol-version,connect-timeout-ms,content-type",
			)
			response := httptest.NewRecorder()
			proxy.server.Handler.ServeHTTP(response, request)
			if !test.allowed {
				if value := response.Header().Get("Access-Control-Allow-Origin"); value != "" {
					t.Fatalf("unconfigured origin allowed: %q", value)
				}
				return
			}
			if response.Code != http.StatusNoContent ||
				response.Header().Get("Access-Control-Allow-Origin") != test.origin {
				t.Fatalf("preflight: status %d, headers %v", response.Code, response.Header())
			}
			allowedHeaders := strings.ToLower(response.Header().Get("Access-Control-Allow-Headers"))
			for _, header := range []string{"authorization", "content-type", "connect-protocol-version", "connect-timeout-ms"} {
				if !strings.Contains(allowedHeaders, header) {
					t.Fatalf("preflight omitted %s: %v", header, response.Header())
				}
			}
			body := strings.NewReader("{}")
			request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, body)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Content-Type", "application/json")
			response = httptest.NewRecorder()
			proxy.server.Handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized ||
				response.Header().Get("Access-Control-Allow-Origin") != test.origin {
				t.Fatalf("unauthenticated response: status %d, headers %v", response.Code, response.Header())
			}
			exposed := strings.ToLower(response.Header().Get("Access-Control-Expose-Headers"))
			for _, header := range []string{"grpc-status", "grpc-message", "grpc-status-details-bin"} {
				if !strings.Contains(exposed, header) {
					t.Fatalf("response omitted %s: %v", header, response.Header())
				}
			}
		})
	}
}
