package registrytoken_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"reflect"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/team-loco/loco/api/pkg/registrytoken"
)

const (
	testKeyID    = "v2"
	testRepo     = "builds/ws-0199e0a4/api"
	testService  = "loco-registry"
	testIssuer   = "https://api.loco.test"
	testSubject  = "0199e0a4-1111-7000-8000-000000000001"
	testBuildID  = "0199e0a4-2222-7000-8000-000000000002"
	testTokenID  = "token-id"
	testLifetime = 5 * time.Minute
)

type keySigner struct {
	key ed25519.PrivateKey
}

func (s keySigner) Sign(_ context.Context, _ string, input []byte) ([]byte, error) {
	return ed25519.Sign(s.key, input), nil
}

func publicKeyFromPEM(t *testing.T, encoded []byte) ed25519.PublicKey {
	t.Helper()
	block, rest := pem.Decode(encoded)
	if block == nil || block.Type != "PUBLIC KEY" || len(rest) != 0 {
		t.Fatalf("pem = %q", encoded)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		t.Fatalf("pem key is a %T, want ed25519.PublicKey", parsed)
	}
	return key
}

func TestSignedTokenVerifiesWithThePEMKeyAndCarriesTheAccess(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := registrytoken.EncodePublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	access := []registrytoken.Access{
		{
			Type:    registrytoken.TypeRepository,
			Name:    testRepo,
			Actions: []string{registrytoken.ActionPull, registrytoken.ActionPush},
		},
	}
	claims := registrytoken.Claims{
		Issuer:    testIssuer,
		Subject:   testSubject,
		Audience:  jwt.Audience{testService},
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		Expiry:    jwt.NewNumericDate(now.Add(testLifetime)),
		ID:        testTokenID,
		Access:    access,
		Build:     testBuildID,
	}
	token, err := registrytoken.Sign(t.Context(), keySigner{key: private}, testKeyID, claims)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		t.Fatal(err)
	}
	if kid := parsed.Headers[0].KeyID; kid != testKeyID {
		t.Fatalf("kid = %q, want %q", kid, testKeyID)
	}
	var raw map[string]json.RawMessage
	if err := parsed.Claims(publicKeyFromPEM(t, encoded), &raw); err != nil {
		t.Fatalf("verify with the PEM key: %v", err)
	}
	var got registrytoken.Claims
	if err := parsed.Claims(public, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, claims) {
		t.Fatalf("claims = %+v, want %+v", got, claims)
	}
	if aud := string(raw["aud"]); aud != `"`+testService+`"` {
		t.Fatalf("aud = %s, want a single string", aud)
	}
	expected := jwt.Expected{Issuer: testIssuer, AnyAudience: jwt.Audience{testService}, Time: now}
	if err := got.Validate(expected); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestSignedTokenWithoutAccessCarriesAnEmptyList(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token, err := registrytoken.Sign(t.Context(), keySigner{key: private}, testKeyID, registrytoken.Claims{})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := parsed.UnsafeClaimsWithoutVerification(&raw); err != nil {
		t.Fatal(err)
	}
	if access := string(raw["access"]); access != "[]" {
		t.Fatalf("access = %s, want []", access)
	}
	if _, ok := raw["loco.build"]; ok {
		t.Fatalf("claims = %v, want no loco.build", raw)
	}
}
