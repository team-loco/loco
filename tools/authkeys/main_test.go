package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/go-jose/go-jose/v4"
)

func TestGenerateProviderKeys(t *testing.T) {
	keys, err := generateProviderKeys()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var set []jose.JSONWebKey
	if jsonErr := json.Unmarshal([]byte(keys.JWTKeys), &set); jsonErr != nil {
		t.Fatalf("jwt keys are not a JWK list: %v", jsonErr)
	}
	if len(set) != 1 || set[0].IsPublic() || !set[0].Valid() || set[0].KeyID == "" || set[0].Algorithm != "RS256" {
		t.Fatalf("jwt key = %+v", set)
	}

	if keys.JWTSecret == "" {
		t.Fatal("empty jwt secret")
	}
}

func TestServiceRoleKeyFromJWKs(t *testing.T) {
	keys, err := generateProviderKeys()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	token, err := serviceRoleKeyFromJWKs(keys.JWTKeys)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if strings.Count(token, ".") != 2 {
		t.Fatalf("token = %q", token)
	}
	if _, err := serviceRoleKeyFromJWKs("[]"); !errors.Is(err, errNoRSAKey) {
		t.Fatalf("minted without a key: %v", err)
	}
}
