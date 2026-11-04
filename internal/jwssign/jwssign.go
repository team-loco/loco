package jwssign

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-jose/go-jose/v4"
)

const (
	tokenType  = "JWT"
	segmentSep = "."
)

var ErrSignature = errors.New("signer returned no Ed25519 signature")

var encoding = base64.RawURLEncoding

type Signer interface {
	Sign(ctx context.Context, keyID string, input []byte) ([]byte, error)
}

type header struct {
	Algorithm jose.SignatureAlgorithm `json:"alg"`
	Type      string                  `json:"typ"`
	KeyID     string                  `json:"kid"`
}

func Sign(ctx context.Context, signer Signer, keyID string, claims any) (string, error) {
	encodedHeader, err := encodeSegment(header{Algorithm: jose.EdDSA, Type: tokenType, KeyID: keyID})
	if err != nil {
		return "", err
	}
	encodedClaims, err := encodeSegment(claims)
	if err != nil {
		return "", err
	}
	input := encodedHeader + segmentSep + encodedClaims
	signature, err := signer.Sign(ctx, keyID, []byte(input))
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	if len(signature) != ed25519.SignatureSize {
		return "", fmt.Errorf("%w: %d bytes", ErrSignature, len(signature))
	}
	return input + segmentSep + encoding.EncodeToString(signature), nil
}

func encodeSegment(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode token: %w", err)
	}
	return encoding.EncodeToString(encoded), nil
}
