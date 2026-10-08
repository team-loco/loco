package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"
)

const (
	rsaKeyBits             = 2048
	secretBytes            = 32
	standardWebhooksPrefix = "v1,whsec_"
	serviceRoleYears       = 10
	serviceRoleCommand     = "service-role"
	jwtKeysEnv             = "GOTRUE_JWT_KEYS"
)

var errNoRSAKey = errors.New("no RSA private key in the jwt keys")

type providerKeys struct {
	ServiceRoleKey string
	JWTKeys        string
	SAMLPrivateKey string
	HookSecret     string
	JWTSecret      string
}

func generateProviderKeys() (providerKeys, error) {
	jwtKey, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return providerKeys{}, fmt.Errorf("generate jwt key: %w", err)
	}
	jwtKey.Precompute()
	kid := uuid.NewString()
	b64 := base64.RawURLEncoding.EncodeToString
	exponent := big.NewInt(int64(jwtKey.E))
	jwk := map[string]any{
		"kty":     "RSA",
		"kid":     kid,
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

	samlKey, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return providerKeys{}, fmt.Errorf("generate saml key: %w", err)
	}

	hook := make([]byte, secretBytes)
	if _, hookErr := rand.Read(hook); hookErr != nil {
		return providerKeys{}, fmt.Errorf("generate hook secret: %w", hookErr)
	}
	secret := make([]byte, secretBytes)
	if _, secretErr := rand.Read(secret); secretErr != nil {
		return providerKeys{}, fmt.Errorf("generate jwt secret: %w", secretErr)
	}

	serviceRole, err := signServiceRole(jwtKey, kid)
	if err != nil {
		return providerKeys{}, err
	}

	samlDER := x509.MarshalPKCS1PrivateKey(samlKey)
	return providerKeys{
		ServiceRoleKey: serviceRole,
		JWTKeys:        string(keys),
		SAMLPrivateKey: base64.StdEncoding.EncodeToString(samlDER),
		HookSecret:     standardWebhooksPrefix + base64.StdEncoding.EncodeToString(hook),
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
	expiry := now.AddDate(serviceRoleYears, 0, 0)
	token, err := jwt.Signed(signer).Claims(map[string]any{
		"role": "service_role",
		"iat":  now.Unix(),
		"exp":  expiry.Unix(),
	}).Serialize()
	if err != nil {
		return "", fmt.Errorf("sign service role: %w", err)
	}
	return token, nil
}

func serviceRoleKeyFromJWKs(raw string) (string, error) {
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
	return "", errNoRSAKey
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == serviceRoleCommand {
		key, err := serviceRoleKeyFromJWKs(os.Getenv(jwtKeysEnv))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("AUTH_SUPABASE_SERVICE_KEY='%s'\n", key)
		return
	}
	keys, err := generateProviderKeys()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("GOTRUE_JWT_KEYS='%s'\n", keys.JWTKeys)
	fmt.Printf("GOTRUE_JWT_SECRET='%s'\n", keys.JWTSecret)
	fmt.Printf("GOTRUE_SAML_PRIVATE_KEY='%s'\n", keys.SAMLPrivateKey)
	fmt.Printf("AUTH_HOOK_SECRET='%s'\n", keys.HookSecret)
	fmt.Printf("AUTH_SUPABASE_SERVICE_KEY='%s'\n", keys.ServiceRoleKey)
}
