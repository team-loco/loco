package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/google/uuid"
)

type ProviderKeys struct {
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
	b64 := base64.RawURLEncoding.EncodeToString
	jwk := map[string]any{
		"kty":     "RSA",
		"kid":     uuid.NewString(),
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
	if _, err := rand.Read(hook); err != nil {
		return ProviderKeys{}, fmt.Errorf("generate hook secret: %w", err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return ProviderKeys{}, fmt.Errorf("generate jwt secret: %w", err)
	}

	return ProviderKeys{
		JWTKeys:        string(keys),
		SAMLPrivateKey: base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(samlKey)),
		HookSecret:     "v1,whsec_" + base64.StdEncoding.EncodeToString(hook),
		JWTSecret:      b64(secret),
	}, nil
}
