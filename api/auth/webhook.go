package auth

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

const webhookTolerance = 5 * time.Minute

var ErrWebhookSignature = errors.New("webhook signature is invalid")

type WebhookVerifier struct {
	keys [][]byte
	now  func() time.Time
}

func ParseWebhookSecrets(raw string) (*WebhookVerifier, error) {
	v := &WebhookVerifier{now: time.Now}
	for secret := range strings.FieldsSeq(strings.ReplaceAll(raw, "|", " ")) {
		encoded, ok := strings.CutPrefix(secret, "v1,whsec_")
		if !ok {
			return nil, errors.New("webhook secrets must look like v1,whsec_<base64>")
		}
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("webhook secret is not base64: %w", err)
		}
		if len(key) < 24 {
			return nil, errors.New("webhook secret is shorter than 24 bytes")
		}
		v.keys = append(v.keys, key)
	}
	if len(v.keys) == 0 {
		return nil, errors.New("no webhook secret configured")
	}
	return v, nil
}

func (v *WebhookVerifier) Verify(r *http.Request, maxBody int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > maxBody {
		return nil, errors.New("webhook body is too large")
	}
	id := r.Header.Get("Webhook-Id")
	ts := r.Header.Get("Webhook-Timestamp")
	sigs := r.Header.Get("Webhook-Signature")
	if id == "" || ts == "" || sigs == "" {
		return nil, fmt.Errorf("%w: missing headers", ErrWebhookSignature)
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: bad timestamp", ErrWebhookSignature)
	}
	sent := time.Unix(sec, 0)
	if age := v.now().Sub(sent); age > webhookTolerance || age < -webhookTolerance {
		return nil, fmt.Errorf("%w: timestamp outside tolerance", ErrWebhookSignature)
	}
	signed := []byte(id + "." + ts + "." + string(body))
	for _, key := range v.keys {
		mac := hmac.New(sha256.New, key)
		mac.Write(signed)
		want := mac.Sum(nil)
		for sig := range strings.FieldsSeq(sigs) {
			encoded, ok := strings.CutPrefix(sig, "v1,")
			if !ok {
				continue
			}
			got, decodeErr := base64.StdEncoding.DecodeString(encoded)
			if decodeErr != nil {
				continue
			}
			if hmac.Equal(got, want) {
				return body, nil
			}
		}
	}
	return nil, ErrWebhookSignature
}
