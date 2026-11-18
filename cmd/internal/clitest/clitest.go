// Package clitest signs a test's CLI commands in to a fake API.
package clitest

import (
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/team-loco/loco/cmd/internal/host"
	"github.com/team-loco/loco/cmd/internal/keychain"
)

// Session serves the handlers register adds, points LOCO_HOST at the server and stores a
// file-backed login for it in a fresh HOME. It returns the bearer credential the server receives.
func Session(t *testing.T, register func(*connect.Server)) string {
	t.Helper()
	server := connect.NewServer()
	register(server)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	endpoint := httptest.NewServer(mux)
	t.Cleanup(endpoint.Close)

	t.Setenv("HOME", t.TempDir())
	t.Setenv(keychain.StoreEnvVar, "file")
	t.Setenv(host.LocoHostEnv, endpoint.URL)
	store, err := keychain.ForCurrentUser()
	if err != nil {
		t.Fatal(err)
	}
	credential := rand.Text()
	token := keychain.UserToken{Host: endpoint.URL, Token: credential, ExpiresAt: time.Now().Add(time.Hour)}
	if err := store.Set(token); err != nil {
		t.Fatal(err)
	}
	return credential
}
