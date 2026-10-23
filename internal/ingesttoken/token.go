package ingesttoken

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const (
	Audience = "loco-ingest"
	JWKSPath = "/.well-known/loco-ingest-jwks.json"

	keysSeparator = ","
	keyIDSep      = ":"
)

var (
	ErrInvalid       = errors.New("invalid ingest token")
	ErrExpired       = errors.New("ingest token expired")
	ErrUnknownKey    = errors.New("ingest token signed by an unknown key")
	ErrMissingClaims = errors.New("ingest token lacks sub, jti, iat or exp")
	ErrNoKeys        = errors.New("no ingest signing keys")
	ErrKeyFormat     = errors.New("ingest signing key must be <id>:<base64 Ed25519 seed>")
	ErrDuplicateKey  = errors.New("duplicate ingest signing key ID")
)

var algorithms = []jose.SignatureAlgorithm{jose.EdDSA}

type SigningKey struct {
	ID  string
	Key ed25519.PrivateKey
}

type Claims struct {
	ClusterID string
	Expiry    time.Time
}

func ParseSigningKeys(raw string) ([]SigningKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrNoKeys
	}
	entries := strings.Split(raw, keysSeparator)
	keys := make([]SigningKey, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		id, encoded, ok := strings.Cut(strings.TrimSpace(entry), keyIDSep)
		if !ok || id == "" {
			return nil, ErrKeyFormat
		}
		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateKey, id)
		}
		seen[id] = struct{}{}
		seed, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("%w: key %q", ErrKeyFormat, id)
		}
		keys = append(keys, SigningKey{ID: id, Key: ed25519.NewKeyFromSeed(seed)})
	}
	return keys, nil
}

func Sign(key SigningKey, clusterID string, now time.Time, lifetime time.Duration) (string, time.Time, error) {
	signingKey := jose.SigningKey{Algorithm: jose.EdDSA, Key: jose.JSONWebKey{Key: key.Key, KeyID: key.ID}}
	options := (&jose.SignerOptions{}).WithType("JWT")
	signer, err := jose.NewSigner(signingKey, options)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("create signer: %w", err)
	}
	expiry := now.Add(lifetime)
	claims := jwt.Claims{
		Subject:  clusterID,
		Audience: jwt.Audience{Audience},
		IssuedAt: jwt.NewNumericDate(now),
		Expiry:   jwt.NewNumericDate(expiry),
		ID:       rand.Text(),
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign ingest token: %w", err)
	}
	return token, expiry, nil
}

func PublicKeySet(keys []SigningKey) jose.JSONWebKeySet {
	set := jose.JSONWebKeySet{Keys: make([]jose.JSONWebKey, 0, len(keys))}
	for _, key := range keys {
		public := key.Key.Public()
		set.Keys = append(set.Keys, jose.JSONWebKey{
			Key:       public,
			KeyID:     key.ID,
			Algorithm: string(jose.EdDSA),
			Use:       "sig",
		})
	}
	return set
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
