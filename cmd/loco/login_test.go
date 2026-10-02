package loco

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/team-loco/loco/internal/api"
)

const authorizationPending = "authorization_pending"

func deviceFlowServer(t *testing.T, responses ...AuthTokenResponse) (*api.Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := int(calls.Add(1)) - 1
		if n >= len(responses) {
			n = len(responses) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(responses[n]); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	client := api.NewClient(srv.URL)
	return client, &calls
}

func TestPollAuthTokenWaitsForAuthorization(t *testing.T) {
	client, calls := deviceFlowServer(t,
		AuthTokenResponse{Error: authorizationPending},
		AuthTokenResponse{Error: authorizationPending},
		AuthTokenResponse{AccessToken: "gh-token"},
	)

	token, err := pollAuthToken(context.Background(), client, "client", "device", 0)
	if err != nil {
		t.Fatalf("pollAuthToken: %v", err)
	}
	if token.AccessToken != "gh-token" {
		t.Fatalf("AccessToken = %q, want gh-token", token.AccessToken)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("polled %d times, want 3", got)
	}
}

func TestPollAuthTokenStopsOnTerminalErrors(t *testing.T) {
	for _, code := range []string{"access_denied", "expired_token"} {
		t.Run(code, func(t *testing.T) {
			client, calls := deviceFlowServer(t, AuthTokenResponse{Error: code, ErrorDescription: "nope"})

			_, err := pollAuthToken(context.Background(), client, "client", "device", 0)
			if err == nil || !strings.Contains(err.Error(), code) {
				t.Fatalf("expected an error mentioning %s, got %v", code, err)
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("polled %d times after a terminal error, want 1", got)
			}
		})
	}
}

func TestPollAuthTokenStopsWhenCancelled(t *testing.T) {
	client, _ := deviceFlowServer(t, AuthTokenResponse{Error: authorizationPending})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := pollAuthToken(ctx, client, "client", "device", 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
