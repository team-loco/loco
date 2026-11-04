package jwssign_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/team-loco/loco/internal/jwssign"
)

const testKeyID = "v3"

var errSignerDown = errors.New("signer unavailable")

type keySigner struct {
	key ed25519.PrivateKey
}

func (s keySigner) Sign(_ context.Context, _ string, input []byte) ([]byte, error) {
	return ed25519.Sign(s.key, input), nil
}

type fixedSigner struct {
	signature []byte
	err       error
}

func (s fixedSigner) Sign(context.Context, string, []byte) ([]byte, error) {
	return s.signature, s.err
}

func TestSignProducesAnEdDSAJWTThatVerifiesWithTheKey(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	want := jwt.Claims{Subject: "cluster", ID: "token-id"}
	token, err := jwssign.Sign(t.Context(), keySigner{key: private}, testKeyID, want)
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
	if typ := parsed.Headers[0].ExtraHeaders[jose.HeaderType]; typ != "JWT" {
		t.Fatalf("typ = %v, want JWT", typ)
	}
	var got jwt.Claims
	if err := parsed.Claims(public, &got); err != nil {
		t.Fatal(err)
	}
	if got.Subject != want.Subject || got.ID != want.ID {
		t.Fatalf("claims = %+v, want %+v", got, want)
	}
}

func TestSignFailsWithoutAnEd25519Signature(t *testing.T) {
	_, err := jwssign.Sign(t.Context(), fixedSigner{err: errSignerDown}, testKeyID, jwt.Claims{})
	if !errors.Is(err, errSignerDown) {
		t.Fatalf("unavailable signer = %v", err)
	}
	_, err = jwssign.Sign(t.Context(), fixedSigner{signature: []byte("short")}, testKeyID, jwt.Claims{})
	if !errors.Is(err, jwssign.ErrSignature) {
		t.Fatalf("short signature = %v", err)
	}
}
