package webhooks_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	standardSecretPrefix    = "whsec_"
	standardSignaturePrefix = "v1,"
	standardTolerance       = 5 * time.Minute
)

var (
	errMissingHeaders = errors.New("webhook headers are missing")
	errStaleTimestamp = errors.New("webhook timestamp is outside the tolerance")
	errBadSignature   = errors.New("webhook signature does not match")
	errBodyTooLarge   = errors.New("webhook body is too large")
)

type standardVerifier struct {
	key []byte
}

func newStandardVerifier(secret string) (*standardVerifier, error) {
	encoded, ok := strings.CutPrefix(secret, standardSecretPrefix)
	if !ok {
		return nil, fmt.Errorf("secret %q has no %s prefix", secret, standardSecretPrefix)
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode secret: %w", err)
	}
	return &standardVerifier{key: key}, nil
}

func (v *standardVerifier) Verify(r *http.Request, maxBody int64) ([]byte, error) {
	limited := io.LimitReader(r.Body, maxBody+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > maxBody {
		return nil, errBodyTooLarge
	}
	id := r.Header.Get("Webhook-Id")
	timestamp := r.Header.Get("Webhook-Timestamp")
	signatures := r.Header.Get("Webhook-Signature")
	if id == "" || timestamp == "" || signatures == "" {
		return nil, errMissingHeaders
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse timestamp: %w", err)
	}
	sent := time.Unix(seconds, 0)
	age := time.Since(sent).Abs()
	if age > standardTolerance {
		return nil, errStaleTimestamp
	}
	if !v.matches(id, timestamp, body, signatures) {
		return nil, errBadSignature
	}
	return body, nil
}

func (v *standardVerifier) matches(id, timestamp string, body []byte, signatures string) bool {
	mac := hmac.New(sha256.New, v.key)
	mac.Write([]byte(id + "." + timestamp + "."))
	mac.Write(body)
	want := mac.Sum(nil)
	for signature := range strings.FieldsSeq(signatures) {
		encoded, ok := strings.CutPrefix(signature, standardSignaturePrefix)
		if !ok {
			continue
		}
		got, err := base64.StdEncoding.DecodeString(encoded)
		if err == nil && hmac.Equal(got, want) {
			return true
		}
	}
	return false
}
