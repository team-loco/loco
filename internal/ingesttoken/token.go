package ingesttoken

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const (
	Audience = "loco-ingest"
	JWKSPath = "/.well-known/loco-ingest-jwks.json"

	tokenType  = "JWT"
	keyUse     = "sig"
	segmentSep = "."
)

var (
	ErrInvalid       = errors.New("invalid ingest token")
	ErrExpired       = errors.New("ingest token expired")
	ErrUnknownKey    = errors.New("ingest token signed by an unknown key")
	ErrMissingClaims = errors.New("ingest token lacks sub, jti, iat or exp")
	ErrSignature     = errors.New("ingest token signer returned no Ed25519 signature")
)

var (
	algorithms = []jose.SignatureAlgorithm{jose.EdDSA}
	encoding   = base64.RawURLEncoding
)

type Signer interface {
	KeyID(ctx context.Context) (string, error)
	Sign(ctx context.Context, keyID string, input []byte) ([]byte, error)
}

type Claims struct {
	ClusterID string
	Expiry    time.Time
}

type header struct {
	Algorithm jose.SignatureAlgorithm `json:"alg"`
	Type      string                  `json:"typ"`
	KeyID     string                  `json:"kid"`
}

func Sign(
	ctx context.Context,
	signer Signer,
	clusterID string,
	now time.Time,
	lifetime time.Duration,
) (string, time.Time, error) {
	keyID, err := signer.KeyID(ctx)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("ingest signing key: %w", err)
	}
	expiry := now.Add(lifetime)
	claims := jwt.Claims{
		Subject:  clusterID,
		Audience: jwt.Audience{Audience},
		IssuedAt: jwt.NewNumericDate(now),
		Expiry:   jwt.NewNumericDate(expiry),
		ID:       rand.Text(),
	}
	encodedHeader, err := encodeSegment(header{Algorithm: jose.EdDSA, Type: tokenType, KeyID: keyID})
	if err != nil {
		return "", time.Time{}, err
	}
	encodedClaims, err := encodeSegment(claims)
	if err != nil {
		return "", time.Time{}, err
	}
	input := encodedHeader + segmentSep + encodedClaims
	signature, err := signer.Sign(ctx, keyID, []byte(input))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign ingest token: %w", err)
	}
	if len(signature) != ed25519.SignatureSize {
		return "", time.Time{}, fmt.Errorf("%w: %d bytes", ErrSignature, len(signature))
	}
	return input + segmentSep + encoding.EncodeToString(signature), expiry, nil
}

func encodeSegment(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode ingest token: %w", err)
	}
	return encoding.EncodeToString(encoded), nil
}

func PublicKey(keyID string, key ed25519.PublicKey) jose.JSONWebKey {
	return jose.JSONWebKey{Key: key, KeyID: keyID, Algorithm: string(jose.EdDSA), Use: keyUse}
}

func Verify(token string, keys jose.JSONWebKeySet, now time.Time) (Claims, error) {
	parsed, err := jwt.ParseSigned(token, algorithms)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	matches := keys.Key(parsed.Headers[0].KeyID)
	if len(matches) == 0 {
		return Claims{}, ErrUnknownKey
	}
	var claims jwt.Claims
	if claimsErr := parsed.Claims(matches[0].Key, &claims); claimsErr != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalid, claimsErr)
	}
	if claims.Subject == "" || claims.ID == "" || claims.IssuedAt == nil || claims.Expiry == nil {
		return Claims{}, ErrMissingClaims
	}
	expected := jwt.Expected{AnyAudience: jwt.Audience{Audience}, Time: now}
	err = claims.ValidateWithLeeway(expected, jwt.DefaultLeeway)
	if errors.Is(err, jwt.ErrExpired) {
		return Claims{}, ErrExpired
	}
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return Claims{ClusterID: claims.Subject, Expiry: claims.Expiry.Time()}, nil
}
