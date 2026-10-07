package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const configuredOrigin = "https://app.loco.build"

func preflightAllowed(t *testing.T, h http.Handler, origin string) bool {
	t.Helper()
	req := httptest.NewRequest(http.MethodOptions, "/loco.user.v1.UserService/WhoAmI", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Header().Get("Access-Control-Allow-Origin") == origin
}

func TestWithCORS(t *testing.T) {
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})
	tests := []struct {
		name          string
		allowLoopback bool
		origin        string
		want          bool
	}{
		{"configured origin outside production", true, configuredOrigin, true},
		{"configured origin in production", false, configuredOrigin, true},
		{"localhost on any port outside production", true, "http://localhost:5199", true},
		{"ipv4 loopback outside production", true, "http://127.0.0.1:3000", true},
		{"ipv6 loopback outside production", true, "http://[::1]:5173", true},
		{"https localhost outside production", true, "https://localhost", true},
		{"localhost in production", false, "http://localhost:5199", false},
		{"other host outside production", true, "https://evil.example", false},
		{"localhost-prefixed host outside production", true, "http://localhost.evil.example", false},
		{"non-http scheme outside production", true, "file://localhost", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			middleware := withCORS([]string{configuredOrigin}, tt.allowLoopback)
			h := middleware(next)
			if got := preflightAllowed(t, h, tt.origin); got != tt.want {
				t.Errorf("origin %q allowed = %v, want %v", tt.origin, got, tt.want)
			}
		})
	}
}
