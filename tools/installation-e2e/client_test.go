package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fixtureTokenServer stands in for Dex's token endpoint and issues idToken for the fixture user.
func fixtureTokenServer(t *testing.T, idToken string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if checkErr := r.ParseForm(); checkErr != nil {
			t.Error(checkErr)
		}
		if r.URL.Path != "/token" || r.Form.Get("grant_type") != "password" ||
			r.Form.Get("client_id") != fixtureClient || r.Form.Get("username") != fixtureEmail ||
			r.Form.Get("password") != fixturePassword || !strings.Contains(r.Form.Get("scope"), "openid") {
			http.Error(w, "unexpected token request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"provider-access","token_type":"bearer","id_token":%q}`, idToken)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestFixtureIDTokenUsesThePasswordGrant(t *testing.T) {
	server := fixtureTokenServer(t, "fixture-id-token")
	token, err := fixtureIDToken(t.Context(), server.URL)
	if err != nil || token != "fixture-id-token" {
		t.Fatalf("ID token = %q (%v), want fixture-id-token", token, err)
	}
}

func TestIdPFailureDoesNotDiscloseResponse(t *testing.T) {
	const canary = "provider-credential-canary"
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, canary, http.StatusBadGateway) }),
	)
	defer server.Close()
	_, err := fixtureIDToken(t.Context(), server.URL)
	if err == nil || strings.Contains(err.Error(), canary) {
		t.Fatalf("unsafe IdP error: %v", err)
	}
}
