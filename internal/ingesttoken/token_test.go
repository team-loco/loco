package ingesttoken

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const (
	testClusterID = "0199e0a4-1111-7000-8000-000000000001"
	testLifetime  = time.Hour
	currentKeyID  = "current"
	previousKeyID = "previous"
)

func testKey(t *testing.T, id string) SigningKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return SigningKey{ID: id, Key: key}
}

func encodedKey(key SigningKey) string {
	return key.ID + keyIDSep + base64.StdEncoding.EncodeToString(key.Key.Seed())
}

func TestParseSigningKeysKeepsOrder(t *testing.T) {
	current := testKey(t, currentKeyID)
	previous := testKey(t, previousKeyID)
	keys, err := ParseSigningKeys(encodedKey(current) + keysSeparator + " " + encodedKey(previous))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].ID != currentKeyID || !keys[1].Key.Equal(previous.Key) {
		t.Fatalf("keys = %v", keys)
	}
}

func TestParseSigningKeysRejectsInvalidInput(t *testing.T) {
	key := testKey(t, currentKeyID)
	cases := map[string]error{
		"":             ErrNoKeys,
		"no-separator": ErrKeyFormat,
		":" + encodedKey(key)[len(currentKeyID)+1:]: ErrKeyFormat,
		currentKeyID + ":not-base64!":               ErrKeyFormat,
		currentKeyID + ":c2hvcnQ=":                  ErrKeyFormat,
		encodedKey(key) + "," + encodedKey(key):     ErrDuplicateKey,
	}
	for raw, want := range cases {
		if _, err := ParseSigningKeys(raw); !errors.Is(err, want) {
			t.Errorf("ParseSigningKeys(%q) = %v, want %v", raw, err, want)
		}
	}
}

func TestVerifyAcceptsEveryPublishedKey(t *testing.T) {
	current := testKey(t, currentKeyID)
	previous := testKey(t, previousKeyID)
	set := PublicKeySet([]SigningKey{current, previous})
	now := time.Now()
	for _, key := range []SigningKey{current, previous} {
		token, expiry, err := Sign(key, testClusterID, now, testLifetime)
		if err != nil {
			t.Fatal(err)
		}
		claims, err := Verify(token, set, now)
		if err != nil {
			t.Fatalf("verify token from %s: %v", key.ID, err)
		}
		if claims.ClusterID != testClusterID || !claims.Expiry.Equal(expiry.Truncate(time.Second)) {
			t.Fatalf("claims = %+v, expiry %s", claims, expiry)
		}
	}
	rotated := PublicKeySet([]SigningKey{current})
	token, _, err := Sign(previous, testClusterID, now, testLifetime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(token, rotated, now); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("token from a retired key = %v, want ErrUnknownKey", err)
	}
}

func signClaims(t *testing.T, key SigningKey, claims jwt.Claims) string {
	t.Helper()
	signingKey := jose.SigningKey{Algorithm: jose.EdDSA, Key: jose.JSONWebKey{Key: key.Key, KeyID: key.ID}}
	signer, err := jose.NewSigner(signingKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestVerifyDistinguishesExpiredFromInvalid(t *testing.T) {
	key := testKey(t, currentKeyID)
	forger := testKey(t, currentKeyID)
	set := PublicKeySet([]SigningKey{key})
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
		"expired":                 {signClaims(t, key, valid(Audience, past)), ErrExpired},
		"wrong audience":          {signClaims(t, key, valid("loco-api", now)), ErrInvalid},
		"expired, wrong audience": {signClaims(t, key, valid("loco-api", past)), ErrInvalid},
		"forged signature":        {signClaims(t, forger, valid(Audience, now)), ErrInvalid},
		"expired, forged":         {signClaims(t, forger, valid(Audience, past)), ErrInvalid},
		"missing subject": {
			signClaims(t, key, jwt.Claims{Audience: jwt.Audience{Audience}}),
			ErrMissingClaims,
		},
		"issued in the future": {signClaims(t, key, valid(Audience, now.Add(testLifetime))), ErrInvalid},
		"not a token":          {"not-a-token", ErrInvalid},
	}
	for name, tc := range cases {
		if _, err := Verify(tc.token, set, now); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
}
