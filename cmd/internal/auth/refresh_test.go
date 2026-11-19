package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/team-loco/loco/cmd/internal/keychain"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
)

type abortingRefresh struct {
	authv1connect.UnimplementedAuthServiceHandler
	store *memoryStore
}

func (a abortingRefresh) RefreshCLIToken(
	context.Context, *authv1.RefreshCLITokenRequest,
) (*authv1.RefreshCLITokenResponse, error) {
	// A concurrent process stores its tokens, then this call loses the race.
	a.store.token = &keychain.UserToken{
		Host: a.store.token.Host, Token: "rotated", RefreshToken: "rotated-refresh",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	return nil, connect.NewError(connect.CodeAborted, "just rotated")
}

type memoryStore struct{ token *keychain.UserToken }

func (m *memoryStore) Get() (*keychain.UserToken, error) { return m.token, nil }
func (m *memoryStore) Set(t keychain.UserToken) error    { m.token = &t; return nil }
func (m *memoryStore) Delete() error                     { m.token = nil; return nil }

func TestFreshTokenUsesTheTokensAConcurrentRefreshStored(t *testing.T) {
	store := &memoryStore{}
	server := connect.NewServer()
	authv1connect.RegisterAuthServiceHandler(server, abortingRefresh{store: store})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)
	store.token = &keychain.UserToken{
		Host: api.URL, Token: "old", RefreshToken: "old-refresh", ExpiresAt: time.Now().Add(-time.Minute),
	}

	got, err := FreshToken(t.Context(), api.URL, store)
	if err != nil || got.Token != "rotated" {
		t.Fatalf("FreshToken = %+v, %v; want the rotated token", got, err)
	}
}
