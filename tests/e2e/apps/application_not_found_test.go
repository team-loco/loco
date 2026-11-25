package apps

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

func TestApplicationNotFoundEndpoint(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "error-page"
	dir := t.TempDir()
	platform.WriteLocoFile(t, dir, locofile.File{
		Partial:  app,
		Services: map[string]locofile.Service{app: platform.ImageService(p.PublicImage, w.Host(app))},
	})
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")

	for _, tc := range []struct {
		host, title string
	}{
		{w.Host("missing"), "Application not found"},
		{w.Host(app), "Application unavailable"},
	} {
		t.Run(tc.host, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, p.APIURL+"/application-not-found", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("X-Forwarded-Host", tc.host)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(res.Body)
			if closeErr := res.Body.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != http.StatusNotFound || !strings.Contains(string(body), tc.title) {
				t.Fatalf("status %d, body %s", res.StatusCode, body)
			}
		})
	}
}
