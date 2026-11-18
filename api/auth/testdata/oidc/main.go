// This issuer deliberately sends no CORS headers. It exercises dashboard PKCE
// sign-in through a real API without requiring an external identity service.
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func main() {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{
		Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "browser-test"},
	}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		panic(err)
	}
	type login struct{ nonce, challenge, redirect string }
	var mu sync.Mutex
	codes := map[string]login{}
	var grants []string
	var issuer string
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, value any) {
		w.Header().Set("Content-Type", "application/json")
		if encodeErr := json.NewEncoder(w).Encode(value); encodeErr != nil {
			panic(encodeErr)
		}
	}
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		write(w, map[string]any{
			"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
			"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys",
			"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"token_endpoint_auth_methods_supported": []string{"none"},
		})
	})
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, _ *http.Request) {
		write(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: &key.PublicKey, KeyID: "browser-test", Algorithm: "RS256", Use: "sig",
		}}})
	})
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("client_id") != "loco" || q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" {
			http.Error(w, "PKCE authorization required", http.StatusBadRequest)
			return
		}
		redirect, parseErr := url.Parse(q.Get("redirect_uri"))
		if parseErr != nil {
			panic(parseErr)
		}
		code := rand.Text()
		mu.Lock()
		codes[code] = login{q.Get("nonce"), q.Get("code_challenge"), redirect.String()}
		mu.Unlock()
		params := redirect.Query()
		params.Set("code", code)
		params.Set("state", q.Get("state"))
		redirect.RawQuery = params.Encode()
		http.Redirect(w, r, redirect.String(), http.StatusFound)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if parseErr := r.ParseForm(); parseErr != nil {
			http.Error(w, parseErr.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		grant := r.Form.Get("grant_type")
		pending, ok := codes[r.Form.Get("code")]
		delete(codes, r.Form.Get("code"))
		challenge := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		ok = ok && grant == "authorization_code" &&
			base64.RawURLEncoding.EncodeToString(challenge[:]) == pending.challenge &&
			r.Form.Get("redirect_uri") == pending.redirect
		if !ok || r.Form.Get("client_id") != "loco" {
			w.WriteHeader(http.StatusBadRequest)
			write(w, map[string]string{"error": "invalid_grant"})
			return
		}
		grants = append(grants, grant)
		now := time.Now()
		token, signErr := jwt.Signed(signer).Claims(map[string]any{
			"iss": issuer, "sub": "browser-owner", "aud": "loco", "nonce": pending.nonce,
			"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
			"email": "owner@acme.test", "email_verified": true, "name": "Olivia Owner",
		}).Serialize()
		if signErr != nil {
			panic(signErr)
		}
		write(w, map[string]any{
			"access_token": "unused-access-token", "token_type": "Bearer", "expires_in": 3600,
			"id_token": token, "refresh_token": "unused-provider-refresh",
		})
	})
	mux.HandleFunc("GET /grants", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		write(w, grants)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	issuer = server.URL
	fmt.Println(issuer)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}
