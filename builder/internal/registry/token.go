package registry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

const (
	tokenRenewDivisor    = 2
	defaultTokenLifetime = 60 * time.Second
	maxTokenAttempts     = 5
	firstRetryDelay      = time.Second
)

var (
	errTokenRejected      = errors.New("registry rejected every fresh token")
	errExchangeFailed     = errors.New("token exchange failed")
	errCredentialRejected = errors.New("realm rejected the registry credential")
)

type tokenTransport struct {
	inner     http.RoundTripper
	exchange  http.RoundTripper
	registry  name.Registry
	challenge *transport.Challenge
	auth      authn.Authenticator
	scopes    []string
	now       func() time.Time
	sleep     func(context.Context, time.Duration) error

	mu      sync.Mutex
	token   string
	renewAt time.Time
	failed  error
}

func (t *tokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != t.registry.RegistryStr() {
		return t.inner.RoundTrip(req)
	}
	ctx := req.Context()
	for attempt := 1; ; attempt++ {
		token, err := t.current(ctx)
		if err != nil {
			return nil, closeUnsent(req, attempt, err)
		}
		out, err := withToken(req, token, attempt)
		if err != nil {
			return nil, err
		}
		resp, err := t.inner.RoundTrip(out)
		if err != nil || resp.StatusCode != http.StatusUnauthorized {
			return resp, err
		}
		t.discard(token)
		if !replayable(req) {
			return resp, nil
		}
		if closeErr := resp.Body.Close(); closeErr != nil {
			return nil, fmt.Errorf("close rejected response: %w", closeErr)
		}
		if attempt == maxTokenAttempts {
			return nil, fmt.Errorf("%s: %w after %d attempts", t.registry, errTokenRejected, attempt)
		}
		delay := retryDelay(attempt)
		if err := t.sleep(ctx, delay); err != nil {
			return nil, err
		}
	}
}

func (t *tokenTransport) current(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.failed != nil {
		return "", t.failed
	}
	if t.token != "" && t.now().Before(t.renewAt) {
		return t.token, nil
	}
	var err error
	for attempt := 1; attempt <= maxTokenAttempts; attempt++ {
		if attempt > 1 {
			delay := retryDelay(attempt - 1)
			if sleepErr := t.sleep(ctx, delay); sleepErr != nil {
				return "", sleepErr
			}
		}
		issued := t.now()
		var tok *transport.Token
		tok, err = transport.Exchange(ctx, t.registry, t.auth, t.exchange, t.scopes, t.challenge)
		if err == nil {
			t.token = tok.Token
			if t.token == "" {
				t.token = tok.AccessToken
			}
			lifetime := tokenLifetime(tok)
			t.renewAt = issued.Add(lifetime / tokenRenewDivisor)
			return t.token, nil
		}
		realm := t.challenge.Parameters["realm"]
		if CredentialRejected(err) {
			t.failed = fmt.Errorf("%w for %s at %s: %w", errCredentialRejected, t.registry, realm, err)
			return "", t.failed
		}
		if !transient(err) {
			t.failed = fmt.Errorf("%w for %s at %s: %w", errExchangeFailed, t.registry, realm, err)
			return "", t.failed
		}
	}
	t.failed = fmt.Errorf("%w for %s at %s after %d attempts: %w",
		errExchangeFailed, t.registry, t.challenge.Parameters["realm"], maxTokenAttempts, err)
	return "", t.failed
}

func (t *tokenTransport) discard(token string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token == token {
		t.token = ""
	}
}

func closeUnsent(req *http.Request, attempt int, err error) error {
	if attempt > 1 || req.Body == nil {
		return err
	}
	closeErr := req.Body.Close()
	return errors.Join(err, closeErr)
}

func transient(err error) bool {
	if terr, ok := errors.AsType[*transport.Error](err); ok {
		return terr.StatusCode == http.StatusTooManyRequests || terr.StatusCode >= http.StatusInternalServerError
	}
	_, network := errors.AsType[net.Error](err)
	return network || errors.Is(err, io.ErrUnexpectedEOF)
}

func withToken(req *http.Request, token string, attempt int) (*http.Request, error) {
	out := req.Clone(req.Context())
	if attempt > 1 && req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("rewind request body: %w", err)
		}
		out.Body = body
	}
	out.Header.Set("Authorization", "Bearer "+token)
	return out, nil
}

func replayable(req *http.Request) bool {
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

func tokenLifetime(tok *transport.Token) time.Duration {
	if tok.ExpiresIn <= 0 {
		return defaultTokenLifetime
	}
	return time.Duration(tok.ExpiresIn) * time.Second
}

func retryDelay(attempt int) time.Duration {
	return firstRetryDelay << (attempt - 1)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
