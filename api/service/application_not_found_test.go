package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApplicationNotFound(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	client := newDomainClient(t, f)
	if err := client.add("known.example.com"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host string
		want string
	}{
		{"missing.example.com", "Application not found"},
		{"known.example.com", "Application unavailable"},
		{"<script>", "Application not found"},
	} {
		t.Run(tc.host, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/application-not-found", nil)
			req.Header.Set("X-Forwarded-Host", tc.host)
			w := httptest.NewRecorder()
			client.server.ApplicationNotFound(w, req)
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d", w.Code)
			}
			if !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("expected %q in response", tc.want)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("error page must not be cached")
			}
			if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
				t.Fatal("expected HTML response")
			}
			if strings.Contains(w.Body.String(), "<script>") {
				t.Fatal("unexpected script")
			}
		})
	}
}
