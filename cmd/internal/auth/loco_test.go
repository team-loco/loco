package auth

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/host"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

func TestAPITokenLoginDoesNotFollowRedirects(t *testing.T) {
	const fixtureBearer = "test-bearer"
	var redirected, authenticated atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Store(true)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(other.Close)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authenticated.Store(r.Header.Get("Authorization") == "Bearer "+fixtureBearer)
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	t.Cleanup(api.Close)

	t.Setenv("LOCO_TOKEN", fixtureBearer)
	t.Setenv(host.LocoHostEnv, api.URL)
	login, err := ResolveLocoLogin(&cobra.Command{})
	if err != nil {
		t.Fatalf("resolve API token: %v", err)
	}
	_, err = login.Clients.User.WhoAmI(t.Context(), &userv1.WhoAmIRequest{})
	if err == nil || !authenticated.Load() || redirected.Load() {
		t.Fatalf("redirect resulted in error %v, original request authenticated %t, second origin called %t",
			err, authenticated.Load(), redirected.Load())
	}
}
