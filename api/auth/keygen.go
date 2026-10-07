package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"
)

type ProviderKeys struct {
	ServiceRoleKey string
	JWTKeys        string
	SAMLPrivateKey string
	HookSecret     string
	JWTSecret      string
}

func GenerateProviderKeys() (ProviderKeys, error) {
	jwtKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return ProviderKeys{}, fmt.Errorf("generate jwt key: %w", err)
	}
	jwtKey.Precompute()
	kid := uuid.NewString()
	b64 := base64.RawURLEncoding.EncodeToString
	jwk := map[string]any{
		"kty":     "RSA",
		"kid":     kid,
		"alg":     "RS256",
		"use":     "sig",
		"key_ops": []string{"sign", "verify"},
		"n":       b64(jwtKey.N.Bytes()),
		"e":       b64(big.NewInt(int64(jwtKey.E)).Bytes()),
		"d":       b64(jwtKey.D.Bytes()),
		"p":       b64(jwtKey.Primes[0].Bytes()),
		"q":       b64(jwtKey.Primes[1].Bytes()),
		"dp":      b64(jwtKey.Precomputed.Dp.Bytes()),
		"dq":      b64(jwtKey.Precomputed.Dq.Bytes()),
		"qi":      b64(jwtKey.Precomputed.Qinv.Bytes()),
	}
	keys, err := json.Marshal([]any{jwk})
	if err != nil {
		return ProviderKeys{}, fmt.Errorf("encode jwk: %w", err)
	}

	samlKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return ProviderKeys{}, fmt.Errorf("generate saml key: %w", err)
	}

	hook := make([]byte, 32)
	if _, hookErr := rand.Read(hook); hookErr != nil {
		return ProviderKeys{}, fmt.Errorf("generate hook secret: %w", hookErr)
	}
	secret := make([]byte, 32)
	if _, secretErr := rand.Read(secret); secretErr != nil {
		return ProviderKeys{}, fmt.Errorf("generate jwt secret: %w", secretErr)
	}

	serviceRole, err := signServiceRole(jwtKey, kid)
	if err != nil {
		return ProviderKeys{}, err
	}

	return ProviderKeys{
		ServiceRoleKey: serviceRole,
		JWTKeys:        string(keys),
		SAMLPrivateKey: base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(samlKey)),
		HookSecret:     "v1,whsec_" + base64.StdEncoding.EncodeToString(hook),
		JWTSecret:      b64(secret),
	}, nil
}

func signServiceRole(key *rsa.PrivateKey, kid string) (string, error) {
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: kid}},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		return "", fmt.Errorf("service role signer: %w", err)
	}
	now := time.Now()
	token, err := jwt.Signed(signer).Claims(map[string]any{
		"role": "service_role",
		"iat":  now.Unix(),
		"exp":  now.AddDate(10, 0, 0).Unix(),
	}).Serialize()
	if err != nil {
		return "", fmt.Errorf("sign service role: %w", err)
	}
	return token, nil
}

func ServiceRoleKeyFromJWKs(raw string) (string, error) {
	var set []jose.JSONWebKey
	if err := json.Unmarshal([]byte(raw), &set); err != nil {
		return "", fmt.Errorf("decode jwt keys: %w", err)
	}
	for _, k := range set {
		key, ok := k.Key.(*rsa.PrivateKey)
		if ok {
			return signServiceRole(key, k.KeyID)
		}
	}
	return "", errors.New("no RSA private key in the jwt keys")
}
