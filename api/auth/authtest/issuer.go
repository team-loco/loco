package authtest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

type Issuer struct {
	t      *testing.T
	server *httptest.Server
	mu     sync.Mutex
	keys   map[string]*rsa.PrivateKey
	served []string
}

func NewIssuer(t *testing.T) *Issuer {
	t.Helper()
	ti := &Issuer{t: t, keys: map[string]*rsa.PrivateKey{}}
	ti.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ti.mu.Lock()
		defer ti.mu.Unlock()
		set := jose.JSONWebKeySet{}
		for _, kid := range ti.served {
			set.Keys = append(set.Keys, jose.JSONWebKey{
				Key:       &ti.keys[kid].PublicKey,
				KeyID:     kid,
				Algorithm: string(jose.RS256),
				Use:       "sig",
			})
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(set); err != nil {
			t.Errorf("encode jwks: %v", err)
		}
	}))
	t.Cleanup(ti.server.Close)
	ti.AddKey("k1", true)
	return ti
}

func (ti *Issuer) AddKey(kid string, publish bool) {
	ti.t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		ti.t.Fatalf("generate key: %v", err)
	}
	ti.mu.Lock()
	defer ti.mu.Unlock()
	ti.keys[kid] = key
	if publish {
		ti.served = append(ti.served, kid)
	}
}

func (ti *Issuer) Publish(kid string) {
	ti.mu.Lock()
	defer ti.mu.Unlock()
	ti.served = append(ti.served, kid)
}

func (ti *Issuer) URL() string {
	return ti.server.URL
}

func (ti *Issuer) Sign(kid string, claims map[string]any) string {
	ti.t.Helper()
	ti.mu.Lock()
	key := ti.keys[kid]
	ti.mu.Unlock()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: kid}},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		ti.t.Fatalf("signer: %v", err)
	}
	raw, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		ti.t.Fatalf("sign: %v", err)
	}
	return raw
}

func (ti *Issuer) Claims(sub, email string, verified bool) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss":            ti.URL(),
		"aud":            "authenticated",
		"sub":            sub,
		"email":          email,
		"iat":            now.Unix(),
		"exp":            now.Add(time.Hour).Unix(),
		"email_verified": verified,
		"user_metadata": map[string]any{
			"full_name":  "Test User",
			"avatar_url": "https://example.com/a.png",
		},
		"amr": []any{map[string]any{"method": "oauth", "timestamp": now.Unix()}},
	}
}
