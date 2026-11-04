package registrytoken

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/team-loco/loco/internal/jwssign"
)

const (
	KeyPath = "/.well-known/loco-registry-key.pem"

	TypeRepository = "repository"

	ActionPull = "pull"
	ActionPush = "push"

	pemBlockType = "PUBLIC KEY"
)

type Access struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

type Claims struct {
	jwt.Claims
	Access []Access `json:"access"`
	Build  string   `json:"loco.build,omitempty"`
}

func Sign(ctx context.Context, signer jwssign.Signer, keyID string, claims Claims) (string, error) {
	if claims.Access == nil {
		claims.Access = []Access{}
	}
	token, err := jwssign.Sign(ctx, signer, keyID, claims)
	if err != nil {
		return "", fmt.Errorf("sign registry token: %w", err)
	}
	return token, nil
}

func EncodePublicKey(key ed25519.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return nil, fmt.Errorf("encode registry public key: %w", err)
	}
	block := &pem.Block{Type: pemBlockType, Bytes: der}
	return pem.EncodeToMemory(block), nil
}
