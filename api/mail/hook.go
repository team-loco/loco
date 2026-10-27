package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"

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

func newHook(u *url.URL, secret string) (*hookTransport, error) {
	if secret == "" || !validHookSecret(secret) {
		return nil, ErrHookSecret
	}
	return &hookTransport{url: u.String(), secret: secret, client: &http.Client{Timeout: hookTimeout}}, nil
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
	if _, drainErr := io.Copy(io.Discard, io.LimitReader(res.Body, maxHookResponse)); drainErr != nil {
		return fmt.Errorf("read hook response: %w", drainErr)
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("hook returned %s", res.Status)
	}
	return nil
}
