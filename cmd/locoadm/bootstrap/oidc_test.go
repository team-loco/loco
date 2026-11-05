package bootstrap

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/team-loco/loco/cmd/internal/installation"
)

const (
	bootstrapTestEmail  = "admin@example.com"
	bootstrapTestClient = "loco"
)

type browserOutput struct {
	ctx context.Context
	bytes.Buffer
	done chan error
	once sync.Once
}

func (o *browserOutput) Write(data []byte) (int, error) {
	count, err := o.Buffer.Write(data)
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, "http://") {
			o.once.Do(func() {
				go func() {
					request, getErr := http.NewRequestWithContext(o.ctx, http.MethodGet, line, nil)
					if getErr != nil {
						o.done <- getErr
						return
					}
					response, getErr := http.DefaultClient.Do(request)
					if getErr == nil {
						getErr = response.Body.Close()
					}
					o.done <- getErr
				}()
			})
		}
	}
	return count, err
}

func TestOIDCVerifiedOperator(t *testing.T) {
	for _, tc := range []struct {
		name, email                      string
		verified, wrongNonce, tokenError bool
		browserFallback                  bool
		want                             error
	}{
		{name: "verified", email: bootstrapTestEmail, verified: true},
		{name: "manual browser fallback", email: bootstrapTestEmail, verified: true, browserFallback: true},
		{name: "wrong account", email: "other@example.com", verified: true, want: errIdentity},
		{name: "unverified", email: bootstrapTestEmail, want: errIdentity},
		{name: "nonce mismatch", email: bootstrapTestEmail, verified: true, wrongNonce: true, want: errOIDC},
		{name: "safe token failure", email: bootstrapTestEmail, verified: true, tokenError: true, want: errOIDC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			key, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				t.Fatal(err)
			}
			signer, err := jose.NewSigner(
				jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "test"}},
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			var issuer, nonce, challenge string
			mux := http.NewServeMux()
			mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if encodeErr := json.NewEncoder(w).
					Encode(map[string]any{
						"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
						"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys",
						"id_token_signing_alg_values_supported": []string{"RS256"},
					}); encodeErr != nil {
					t.Error(encodeErr)
				}
			})
			mux.HandleFunc("GET /keys", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if encodeErr := json.NewEncoder(w).
					Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
						{Key: &key.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig"},
					}}); encodeErr != nil {
					t.Error(encodeErr)
				}
			})
			mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				nonce = query.Get("nonce")
				challenge = query.Get("code_challenge")
				if query.Get("code_challenge_method") != "S256" || nonce == "" || challenge == "" ||
					query.Get("client_id") != bootstrapTestClient {
					t.Error("authorization missing PKCE or identity binding")
				}
				callback, parseErr := url.Parse(query.Get("redirect_uri"))
				if parseErr != nil {
					t.Error(parseErr)
					return
				}
				callback.RawQuery = url.Values{"state": {query.Get("state")}, "code": {"test-code"}}.Encode()
				http.Redirect(
					w,
					r,
					callback.String(),
					http.StatusFound,
				) // #nosec G710 -- Redirect targets this test's loopback listener.
			})
			mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
				if parseErr := r.ParseForm(); parseErr != nil {
					t.Error(parseErr)
					return
				}
				sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge || r.Form.Get("code") != "test-code" {
					t.Error("code exchange missing correct verifier")
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.tokenError {
					http.Error(
						w,
						`{"error":"invalid_grant","error_description":"secret-canary"}`,
						http.StatusBadRequest,
					)
					return
				}
				tokenNonce := nonce
				if tc.wrongNonce {
					tokenNonce = "wrong"
				}
				raw, signErr := jwt.Signed(signer).
					Claims(map[string]any{
						"iss": issuer, "aud": bootstrapTestClient, "sub": "operator",
						"email": tc.email, "email_verified": tc.verified,
						"exp": time.Now().Add(time.Minute).Unix(), "nonce": tokenNonce,
					}).
					Serialize()
				if signErr != nil {
					t.Error(signErr)
					return
				}
				if encodeErr := json.NewEncoder(w).
					Encode(map[string]any{
						"access_token": "provider-access-canary", "token_type": "Bearer", "id_token": raw,
					}); encodeErr != nil {
					t.Error(encodeErr)
				}
			})
			provider := httptest.NewServer(mux)
			defer provider.Close()
			issuer = provider.URL
			var listenConfig net.ListenConfig
			listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			if err = listener.Close(); err != nil {
				t.Fatal(err)
			}
			output := browserOutput{ctx: t.Context(), done: make(chan error, 1)}
			operator, err := authenticate(
				t.Context(),
				installation.Auth{
					Issuer:               issuer,
					ClientID:             bootstrapTestClient,
					BootstrapRedirectURI: "http://" + address + "/callback",
				},
				bootstrapTestEmail,
				&output,
				!tc.browserFallback,
			)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if browserErr := <-output.done; browserErr != nil {
				t.Fatal(browserErr)
			}
			if tc.want == nil && (operator == "" || !strings.Contains(output.String(), "Subject: operator")) {
				t.Fatal("missing verified operator identity")
			}
			if tc.browserFallback && !strings.Contains(output.String(), "Open the sign-in URL in your browser.") {
				t.Fatal("failed browser launch did not offer manual sign-in")
			}

			if strings.Contains(output.String(), "canary") || (err != nil && strings.Contains(err.Error(), "canary")) {
				t.Fatal("provider secret escaped in output")
			}
		})
	}
}
