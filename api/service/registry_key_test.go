package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/team-loco/loco/api/pkg/registrytoken"
)

func TestRegistryKeyServesThePublicKeyAsPKIXPEM(t *testing.T) {
	pinned, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewRegistryKeyHandler(pinned)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, registrytoken.KeyPath, nil)
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", recorder.Code)
		}
		if contentType := recorder.Header().Get("Content-Type"); contentType != registryKeyContentType {
			t.Fatalf("content type = %q", contentType)
		}
		block, rest := pem.Decode(recorder.Body.Bytes())
		if block == nil || block.Type != "PUBLIC KEY" || len(rest) != 0 {
			t.Fatalf("body = %q, want one PUBLIC KEY block", recorder.Body.String())
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		key, ok := parsed.(ed25519.PublicKey)
		if !ok || !key.Equal(pinned) {
			t.Fatalf("served key = %v, want the pinned version's key", parsed)
		}
	}
}
