package service

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestApplicationNotFoundLogo(t *testing.T) {
	t.Parallel()
	canonical, err := os.ReadFile("../../web/public/logo.svg")
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := applicationNotFoundAssets.ReadFile("application_logo.svg")
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != string(embedded) {
		t.Fatal("fallback logo must match the dashboard wordmark")
	}
}

func TestApplicationNotFound(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	client := newDomainClient(t, f)
	if err := client.add("known.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE clusters SET is_active = false"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host string
		want string
	}{
		{"missing.example.com", "Application not found"},
		{"known.example.com", "Application unavailable"},
		{"KNOWN.EXAMPLE.COM:443", "Application unavailable"},
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
			if !strings.Contains(w.Body.String(), `viewBox="390 185 995 475"`) {
				t.Fatal("expected Loco wordmark")
			}
			if tc.host == "missing.example.com" && !strings.Contains(w.Body.String(), "Please check the URL") {
				t.Fatal("expected URL guidance")
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
