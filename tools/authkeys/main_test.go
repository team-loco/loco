package main

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
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

	der, err := base64.StdEncoding.DecodeString(keys.SAMLPrivateKey)
	if err != nil {
		t.Fatalf("saml key is not base64: %v", err)
	}
	samlKey, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil || samlKey.N.BitLen() < rsaKeyBits {
		t.Fatalf("saml key: %v", err)
	}

	if keys.JWTSecret == "" {
		t.Fatal("empty jwt secret")
	}
}
