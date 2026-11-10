// Package webhooksig signs outbound HTTP requests with the Standard Webhooks scheme.
package webhooksig

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

const (
	secretPrefix = "whsec_"
	secretBytes  = 32

	MaxResponseBytes = 64 << 10
)

var ErrInvalidSecret = errors.New("webhook secret is malformed")

func NewSecret() (string, error) {
	b := make([]byte, secretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate webhook secret: %w", err)
	}
	return secretPrefix + base64.StdEncoding.EncodeToString(b), nil
}

func ValidateSecret(secret string) error {
	_, err := decodeSecret(secret)
	return err
}

func Sign(secret, id string, timestamp time.Time, body []byte) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	signer, err := standardwebhooks.NewWebhookRaw(key)
	if err != nil {
		return "", fmt.Errorf("create signer: %w", err)
	}
	return signer.Sign(id, timestamp, body)
}

func NewSignedRequest(ctx context.Context, url, secret, id string, body []byte) (*http.Request, error) {
	now := time.Now()
	signature, err := Sign(secret, id, now, body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(standardwebhooks.HeaderWebhookID, id)
	req.Header.Set(standardwebhooks.HeaderWebhookTimestamp, strconv.FormatInt(now.Unix(), 10))
	req.Header.Set(standardwebhooks.HeaderWebhookSignature, signature)
	return req, nil
}

func decodeSecret(secret string) ([]byte, error) {
	encoded, ok := strings.CutPrefix(secret, secretPrefix)
	if !ok {
		return nil, ErrInvalidSecret
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != secretBytes {
		return nil, ErrInvalidSecret
	}
	return key, nil
}
