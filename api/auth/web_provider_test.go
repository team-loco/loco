package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebProviderAuthenticatesConfidentialClients(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"client_secret_basic", "client_secret_post"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			const (
				clientID     = "dashboard"
				clientSecret = "test secret"
				code         = "test code"
			)
			var issuer string
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/.well-known/openid-configuration":
					if err := json.NewEncoder(w).Encode(map[string]any{
						"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
						"token_endpoint": issuer + "/token", "token_endpoint_auth_methods_supported": []string{method},
					}); err != nil {
						t.Errorf("encode provider discovery: %v", err)
					}
				case "/token":
					if err := r.ParseForm(); err != nil {
						t.Errorf("parse token request: %v", err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					user, password, basic := r.BasicAuth()
					if method == "client_secret_basic" {
						if !basic || user != clientID || password != "test+secret" || r.Form.Has("client_secret") {
							t.Error("expected the OAuth Basic credentials in the token request header")
						}
					} else if basic || r.Form.Get("client_id") != clientID || r.Form.Get("client_secret") != clientSecret {
						t.Error("expected the OAuth credentials in the token request form")
					}
					w.WriteHeader(http.StatusBadRequest)
					if err := json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": code}); err != nil {
						t.Errorf("encode token error: %v", err)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			issuer = "http://" + server.Listener.Addr().String()
			server.Start()
			t.Cleanup(server.Close)
			provider := NewWebProvider(server.Client(), nil, IssuerConfig{
				Issuer: issuer,
				Web:    &WebConfig{ClientID: clientID, ClientSecret: clientSecret},
			}, "https://app.example.test")
			_, err := provider.Exchange(t.Context(), code, "verifier", "nonce")
			if err == nil || !strings.Contains(err.Error(), "400") || strings.Contains(err.Error(), code) {
				t.Fatalf("expected a sanitized token error, got %v", err)
			}
		})
	}
}

func TestWebProviderChecksCallbackOrigin(t *testing.T) {
	t.Parallel()
	provider := NewWebProvider(nil, nil, IssuerConfig{}, "https://APP.example.test:443/")
	if values, err := provider.CallbackValues("https://app.example.test/auth/callback?state=value"); err != nil || values.Get("state") != "value" {
		t.Fatalf("callback with equivalent origin = %v, %v", values, err)
	}
	for _, callback := range []string{
		"https://other.example.test/auth/callback?state=value",
		"http://app.example.test/auth/callback?state=value",
		"https://app.example.test:444/auth/callback?state=value",
		"https://user@app.example.test/auth/callback?state=value",
		"https://app.example.test/auth%2Fcallback?state=value",
		"https://app.example.test/auth/callback?state=value#fragment",
	} {
		if _, err := provider.CallbackValues(callback); err == nil {
			t.Errorf("accepted invalid callback %s", callback)
		}
	}
}
