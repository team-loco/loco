package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/team-loco/loco/api/webhooks"
)

const (
	hookTimeout     = 10 * time.Second
	maxHookResponse = 64 << 10
)

type hookTransport struct {
	url    string
	secret string
	client *http.Client
}

func newHook(u *url.URL, secret string, tlsConfig *tls.Config) (*hookTransport, error) {
	if secret == "" || !validHookSecret(secret) {
		return nil, ErrHookSecret
	}
	transport := http.DefaultTransport
	if tlsConfig != nil {
		transport = &http.Transport{TLSClientConfig: tlsConfig}
	}
	traced := otelhttp.NewTransport(transport)
	client := &http.Client{Transport: traced, Timeout: hookTimeout}
	return &hookTransport{url: u.String(), secret: secret, client: client}, nil
}

func (t *hookTransport) Send(ctx context.Context, msg Message) error {
	body, err := json.Marshal(msg.Event)
	if err != nil {
		return fmt.Errorf("encode hook payload: %w", err)
	}
	id := uuid.NewString()
	now := time.Now()
	signature, err := webhooks.Sign(t.secret, id, now, body)
	if err != nil {
		return fmt.Errorf("sign hook payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build hook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Loco-Mail")
	req.Header.Set("Webhook-Id", id)
	req.Header.Set("Webhook-Timestamp", strconv.FormatInt(now.Unix(), 10))
	req.Header.Set("Webhook-Signature", signature)
	res, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("post hook: %w", err)
	}
	defer res.Body.Close()
	response, readErr := io.ReadAll(io.LimitReader(res.Body, maxHookResponse))
	if readErr != nil {
		return fmt.Errorf("read hook response: %w", readErr)
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(response, &failure) == nil && failure.Error != "" {
			return errors.New(failure.Error)
		}
		return fmt.Errorf("hook returned %s", res.Status)
	}
	return nil
}
