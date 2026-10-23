package clusterassertion

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"
)

const (
	testLifetime    = time.Minute
	testMaxLifetime = 2 * testLifetime
)

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return public, private
}

func signClaims(t *testing.T, key ed25519.PrivateKey, claims jwt.Claims) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestSignedAssertionVerifies(t *testing.T) {
	public, private := newKey(t)
	clusterID := uuid.NewString()
	now := time.Now()
	token, err := Sign(private, clusterID, now, testLifetime)
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := ClusterID(token)
	if err != nil || issuer != clusterID {
		t.Fatalf("ClusterID = %q, %v", issuer, err)
	}
	claims, err := Verify(token, public, now, testMaxLifetime)
	if err != nil {
		t.Fatal(err)
	}
	if claims.ClusterID != clusterID || claims.ID == "" {
		t.Fatalf("claims = %+v", claims)
	}
	other, err := Sign(private, clusterID, now, testLifetime)
	if err != nil {
		t.Fatal(err)
	}
	otherClaims, err := Verify(other, public, now, testMaxLifetime)
	if err != nil {
		t.Fatal(err)
	}
	if otherClaims.ID == claims.ID {
		t.Fatal("two assertions share a jti")
	}
}

func TestVerifyRejectsInvalidAssertions(t *testing.T) {
	public, private := newKey(t)
	otherPublic, _ := newKey(t)
	clusterID := uuid.NewString()
	now := time.Now()
	valid := jwt.Claims{
		Issuer:   clusterID,
		Subject:  clusterID,
		Audience: jwt.Audience{Audience},
		IssuedAt: jwt.NewNumericDate(now),
		Expiry:   jwt.NewNumericDate(now.Add(testLifetime)),
		ID:       "jti",
	}
	mutate := func(change func(*jwt.Claims)) jwt.Claims {
		claims := valid
		change(&claims)
		return claims
	}
	for _, tc := range []struct {
		name   string
		claims jwt.Claims
		key    ed25519.PublicKey
		want   error
	}{
		{"wrong key", valid, otherPublic, ErrInvalid},
		{"wrong audience", mutate(func(c *jwt.Claims) { c.Audience = jwt.Audience{"other"} }), public, ErrInvalid},
		{"expired", mutate(func(c *jwt.Claims) {
			c.IssuedAt = jwt.NewNumericDate(now.Add(-time.Hour))
			c.Expiry = jwt.NewNumericDate(now.Add(-time.Hour + testLifetime))
		}), public, ErrInvalid},
		{"issued in the future", mutate(func(c *jwt.Claims) {
			c.IssuedAt = jwt.NewNumericDate(now.Add(time.Hour))
			c.Expiry = jwt.NewNumericDate(now.Add(time.Hour + testLifetime))
		}), public, ErrInvalid},
		{"too long-lived", mutate(func(c *jwt.Claims) {
			c.Expiry = jwt.NewNumericDate(now.Add(time.Hour))
		}), public, ErrLifetime},
		{"no jti", mutate(func(c *jwt.Claims) { c.ID = "" }), public, ErrMissingClaims},
		{"no expiry", mutate(func(c *jwt.Claims) { c.Expiry = nil }), public, ErrMissingClaims},
		{"subject differs", mutate(func(c *jwt.Claims) { c.Subject = "other" }), public, ErrSubjectMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := signClaims(t, private, tc.claims)
			if _, err := Verify(token, tc.key, now, testMaxLifetime); !errors.Is(err, tc.want) {
				t.Fatalf("Verify = %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := Verify(strings.Repeat("x", 10), public, now, testMaxLifetime); !errors.Is(err, ErrInvalid) {
		t.Fatalf("garbage token: %v", err)
	}
}
