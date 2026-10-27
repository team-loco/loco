package ingesttoken_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/team-loco/loco/internal/ingesttoken"
	"github.com/team-loco/loco/internal/ingesttoken/ingesttest"
)

const (
	testClusterID = "0199e0a4-1111-7000-8000-000000000001"
	testLifetime  = time.Hour
	currentKeyID  = "v2"
	previousKeyID = "v1"
)

var errSignerDown = errors.New("signer unavailable")

func testSigner(t *testing.T, id string) ingesttest.Signer {
	t.Helper()
	signer, err := ingesttest.NewSigner(id)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func TestSignBuildsAnEdDSAJWTNamingTheSignersKey(t *testing.T) {
	signer := testSigner(t, currentKeyID)
	token, _, err := ingesttoken.Sign(t.Context(), signer, testClusterID, time.Now(), testLifetime)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, _ := strings.Cut(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var header map[string]string
	if err := json.Unmarshal(raw, &header); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": currentKeyID}
	if len(header) != len(want) || header["alg"] != want["alg"] || header["typ"] != want["typ"] ||
		header["kid"] != want["kid"] {
		t.Fatalf("header = %v, want %v", header, want)
	}
}

func TestVerifyAcceptsEveryPublishedKey(t *testing.T) {
	current := testSigner(t, currentKeyID)
	previous := testSigner(t, previousKeyID)
	set := ingesttest.KeySet([]ingesttest.Signer{current, previous})
	now := time.Now()
	for _, signer := range []ingesttest.Signer{current, previous} {
		token, expiry, err := ingesttoken.Sign(t.Context(), signer, testClusterID, now, testLifetime)
		if err != nil {
			t.Fatal(err)
		}
		claims, err := ingesttoken.Verify(token, set, now)
		if err != nil {
			t.Fatalf("verify token from %s: %v", signer.ID, err)
		}
		if claims.ClusterID != testClusterID || !claims.Expiry.Equal(expiry.Truncate(time.Second)) {
			t.Fatalf("claims = %+v, expiry %s", claims, expiry)
		}
	}
	rotated := ingesttest.KeySet([]ingesttest.Signer{current})
	token, _, err := ingesttoken.Sign(t.Context(), previous, testClusterID, now, testLifetime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingesttoken.Verify(token, rotated, now); !errors.Is(err, ingesttoken.ErrUnknownKey) {
		t.Fatalf("token from a retired key = %v, want ErrUnknownKey", err)
	}
}

type failingSigner struct {
	keyErr    error
	signature []byte
}

func (s failingSigner) KeyID(context.Context) (string, error) {
	return currentKeyID, s.keyErr
}

func (s failingSigner) Sign(context.Context, string, []byte) ([]byte, error) {
	return s.signature, nil
}

func TestSignFailsWithoutAnEd25519Signature(t *testing.T) {
	now := time.Now()
	down := failingSigner{keyErr: errSignerDown}
	_, _, err := ingesttoken.Sign(t.Context(), down, testClusterID, now, testLifetime)
	if !errors.Is(err, errSignerDown) {
		t.Fatalf("unavailable signer = %v", err)
	}
	short := failingSigner{signature: []byte("short")}
	_, _, err = ingesttoken.Sign(t.Context(), short, testClusterID, now, testLifetime)
	if !errors.Is(err, ingesttoken.ErrSignature) {
		t.Fatalf("short signature = %v", err)
	}
}

func signClaims(t *testing.T, signer ingesttest.Signer, claims jwt.Claims) string {
	t.Helper()
	signingKey := jose.SigningKey{Algorithm: jose.EdDSA, Key: jose.JSONWebKey{Key: signer.Key, KeyID: signer.ID}}
	joseSigner, err := jose.NewSigner(signingKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwt.Signed(joseSigner).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestVerifyDistinguishesExpiredFromInvalid(t *testing.T) {
	key := testSigner(t, currentKeyID)
	forger := testSigner(t, currentKeyID)
	set := ingesttest.KeySet([]ingesttest.Signer{key})
	now := time.Now()
	valid := func(audience string, issuedAt time.Time) jwt.Claims {
		return jwt.Claims{
			Subject:  testClusterID,
			Audience: jwt.Audience{audience},
			IssuedAt: jwt.NewNumericDate(issuedAt),
			Expiry:   jwt.NewNumericDate(issuedAt.Add(testLifetime)),
			ID:       rand.Text(),
		}
	}
	past := now.Add(-2 * testLifetime)
	cases := map[string]struct {
		token string
		want  error
	}{
		"expired":                 {signClaims(t, key, valid(ingesttoken.Audience, past)), ingesttoken.ErrExpired},
		"wrong audience":          {signClaims(t, key, valid("loco-api", now)), ingesttoken.ErrInvalid},
		"expired, wrong audience": {signClaims(t, key, valid("loco-api", past)), ingesttoken.ErrInvalid},
		"forged signature":        {signClaims(t, forger, valid(ingesttoken.Audience, now)), ingesttoken.ErrInvalid},
		"expired, forged":         {signClaims(t, forger, valid(ingesttoken.Audience, past)), ingesttoken.ErrInvalid},
		"missing subject": {
			signClaims(t, key, jwt.Claims{Audience: jwt.Audience{ingesttoken.Audience}}),
			ingesttoken.ErrMissingClaims,
		},
		"issued in the future": {
			signClaims(t, key, valid(ingesttoken.Audience, now.Add(testLifetime))),
			ingesttoken.ErrInvalid,
		},
		"not a token": {"not-a-token", ingesttoken.ErrInvalid},
	}
	for name, tc := range cases {
		if _, err := ingesttoken.Verify(tc.token, set, now); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
}
