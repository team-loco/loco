package clusterassertion

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const (
	Audience = "loco-api"
	Leeway   = jwt.DefaultLeeway
)

var (
	ErrInvalid         = errors.New("invalid cluster assertion")
	ErrMissingClaims   = errors.New("cluster assertion lacks iss, sub, jti, iat or exp")
	ErrSubjectMismatch = errors.New("cluster assertion subject differs from its issuer")
	ErrLifetime        = errors.New("cluster assertion lifetime exceeds the allowed maximum")
)

var algorithms = []jose.SignatureAlgorithm{jose.EdDSA}

type Claims struct {
	ClusterID string
	ID        string
	Expiry    time.Time
}

func Sign(key ed25519.PrivateKey, clusterID string, now time.Time, lifetime time.Duration) (string, error) {
	signingKey := jose.SigningKey{Algorithm: jose.EdDSA, Key: key}
	options := (&jose.SignerOptions{}).WithType("JWT")
	signer, err := jose.NewSigner(signingKey, options)
	if err != nil {
		return "", fmt.Errorf("create signer: %w", err)
	}
	claims := jwt.Claims{
		Issuer:   clusterID,
		Subject:  clusterID,
		Audience: jwt.Audience{Audience},
		IssuedAt: jwt.NewNumericDate(now),
		Expiry:   jwt.NewNumericDate(now.Add(lifetime)),
		ID:       rand.Text(),
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		return "", fmt.Errorf("sign assertion: %w", err)
	}
	return token, nil
}

func ClusterID(token string) (string, error) {
	parsed, err := jwt.ParseSigned(token, algorithms)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var claims jwt.Claims
	if err := parsed.UnsafeClaimsWithoutVerification(&claims); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if claims.Issuer == "" {
		return "", ErrMissingClaims
	}
	return claims.Issuer, nil
}

func Verify(token string, key ed25519.PublicKey, now time.Time, maxLifetime time.Duration) (Claims, error) {
	parsed, err := jwt.ParseSigned(token, algorithms)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var claims jwt.Claims
	if err := parsed.Claims(key, &claims); err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if claims.Issuer == "" || claims.ID == "" || claims.IssuedAt == nil || claims.Expiry == nil {
		return Claims{}, ErrMissingClaims
	}
	if claims.Subject != claims.Issuer {
		return Claims{}, ErrSubjectMismatch
	}
	expiry := claims.Expiry.Time()
	if expiry.Sub(claims.IssuedAt.Time()) > maxLifetime {
		return Claims{}, ErrLifetime
	}
	expected := jwt.Expected{Issuer: claims.Issuer, AnyAudience: jwt.Audience{Audience}, Time: now}
	if err := claims.ValidateWithLeeway(expected, Leeway); err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return Claims{ClusterID: claims.Issuer, ID: claims.ID, Expiry: expiry}, nil
}
