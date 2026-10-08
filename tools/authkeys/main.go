package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"

	"github.com/google/uuid"
)

const (
	rsaKeyBits  = 2048
	secretBytes = 32
)

type providerKeys struct {
	JWTKeys   string
	JWTSecret string
}

func generateProviderKeys() (providerKeys, error) {
	jwtKey, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return providerKeys{}, fmt.Errorf("generate jwt key: %w", err)
	}
	jwtKey.Precompute()
	b64 := base64.RawURLEncoding.EncodeToString
	exponent := big.NewInt(int64(jwtKey.E))
	jwk := map[string]any{
		"kty":     "RSA",
		"kid":     uuid.NewString(),
		"alg":     "RS256",
		"use":     "sig",
		"key_ops": []string{"sign", "verify"},
		"n":       b64(jwtKey.N.Bytes()),
		"e":       b64(exponent.Bytes()),
		"d":       b64(jwtKey.D.Bytes()),
		"p":       b64(jwtKey.Primes[0].Bytes()),
		"q":       b64(jwtKey.Primes[1].Bytes()),
		"dp":      b64(jwtKey.Precomputed.Dp.Bytes()),
		"dq":      b64(jwtKey.Precomputed.Dq.Bytes()),
		"qi":      b64(jwtKey.Precomputed.Qinv.Bytes()),
	}
	keys, err := json.Marshal([]any{jwk})
	if err != nil {
		return providerKeys{}, fmt.Errorf("encode jwk: %w", err)
	}

	secret := make([]byte, secretBytes)
	if _, secretErr := rand.Read(secret); secretErr != nil {
		return providerKeys{}, fmt.Errorf("generate jwt secret: %w", secretErr)
	}

	return providerKeys{
		JWTKeys:   string(keys),
		JWTSecret: b64(secret),
	}, nil
}

func main() {
	keys, err := generateProviderKeys()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("GOTRUE_JWT_KEYS='%s'\n", keys.JWTKeys)
	fmt.Printf("GOTRUE_JWT_SECRET='%s'\n", keys.JWTSecret)
}
