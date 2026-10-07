package auth

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-jose/go-jose/v4"
)

func TestGenerateProviderKeys(t *testing.T) {
	keys, err := GenerateProviderKeys()
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
	if err != nil || samlKey.N.BitLen() < 2048 {
		t.Fatalf("saml key: %v", err)
	}

	if _, hookErr := ParseWebhookSecrets(keys.HookSecret); hookErr != nil {
		t.Fatalf("hook secret: %v", hookErr)
	}
	if keys.JWTSecret == "" {
		t.Fatal("empty jwt secret")
	}
}

func TestParseIssuersRejectsUnknownFields(t *testing.T) {
	if _, err := ParseIssuers(`[{"issuer":"http://localhost:9999","preset":"supabase"}]`); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestServiceRoleKeyFromJWKs(t *testing.T) {
	keys, err := GenerateProviderKeys()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	token, err := ServiceRoleKeyFromJWKs(keys.JWTKeys)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if strings.Count(token, ".") != 2 {
		t.Fatalf("token = %q", token)
	}
	if _, err := ServiceRoleKeyFromJWKs("[]"); err == nil {
		t.Fatal("minted without a key")
	}
}
