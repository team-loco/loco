package transit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/team-loco/loco/api/pkg/tracing"
)

const (
	lookupSelfPath         = "auth/token/lookup-self"
	renewSelfPath          = "auth/token/renew-self"
	shortTokenRenewDivisor = 2
	rejectionBackoffFactor = 2
	tokenRefreshSpan       = "transit.refresh_token"
)

var (
	ErrRenewal      = errors.New("transit token renewal margin and retry interval must be positive")
	ErrTokenExpired = errors.New("transit token has expired")
	ErrNoLease      = errors.New("transit token renewal returned no lease")
	ErrNotLoggedIn  = errors.New("transit has not logged in yet")
)

type lookupResponse struct {
	Data struct {
		TTL       int64 `json:"ttl"`
		Renewable bool  `json:"renewable"`
	} `json:"data"`
}

type renewResponse struct {
	Auth struct {
		LeaseDuration int64 `json:"lease_duration"`
		Renewable     bool  `json:"renewable"`
	} `json:"auth"`
}

type tokenLease struct {
	token     string
	ttl       time.Duration
	renewable bool
}

func (t *Client) currentToken() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.token
}

func (t *Client) tokenExpiry() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.expiresAt
}

func (t *Client) setTokenLease(lease tokenLease) {
	expiry := time.Time{}
	if lease.ttl > 0 {
		expiry = t.now().Add(lease.ttl)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if lease.token != "" {
		t.token = lease.token
	}
	t.expiresAt = expiry
}

func (t *Client) checkToken() error {
	if t.currentToken() == "" {
		return ErrNotLoggedIn
	}
	expiry := t.tokenExpiry()
	if expiry.IsZero() || t.now().Before(expiry) {
		return nil
	}
	return fmt.Errorf("%w at %s", ErrTokenExpired, expiry.Format(time.RFC3339))
}

func (t *Client) renewalDelay(ttl time.Duration) time.Duration {
	if ttl > t.renewMargin {
		return ttl - t.renewMargin
	}
	return ttl / shortTokenRenewDivisor
}

func (t *Client) tokenRejected() {
	if t.rejected == nil {
		return
	}
	select {
	case t.rejected <- struct{}{}:
	default:
	}
}

func (t *Client) rejectionBackoff() time.Duration {
	rejections := t.rejections.Add(1)
	backoff := t.renewRetry
	for range rejections - 1 {
		if backoff >= t.renewMargin {
			break
		}
		backoff *= rejectionBackoffFactor
	}
	return min(backoff, t.renewMargin)
}

func (t *Client) drainRejections() {
	select {
	case <-t.rejected:
	default:
	}
}

// logIn makes the first token lookup, with token auth, or the first login, with Kubernetes
// auth, and stores the lease. While Transit is unreachable or answers with a server error it
// retries every renewRetry until ctx is done; any other failure is returned at once.
func (t *Client) logIn(ctx context.Context) (tokenLease, error) {
	refresh := t.lookupToken
	if t.kubernetes != nil {
		refresh = t.login
	}
	for {
		lease, err := refresh(ctx)
		if err == nil {
			t.setTokenLease(lease)
			return lease, nil
		}
		if !isUnavailable(err) {
			return tokenLease{}, err
		}
		slog.WarnContext(ctx, "transit is unavailable; logging in again", "error", err, "in", t.renewRetry)
		if !sleepContext(ctx, t.renewRetry) {
			return tokenLease{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
	}
}

func isUnavailable(err error) bool {
	if status, ok := errors.AsType[*statusError](err); ok {
		return status.code >= http.StatusInternalServerError
	}
	return errors.Is(err, ErrRequest)
}

// renewToken keeps the token of lease valid until ctx is done. With token auth it renews
// the token renewMargin before each expiry, and stops when the token has no TTL or can no
// longer be renewed. With Kubernetes auth it logs in again renewMargin before each expiry,
// or after rejectionBackoff when the server rejects the token. A failed renewal or login is
// logged and retried every renewRetry.
func (t *Client) renewToken(ctx context.Context, lease tokenLease) {
	refresh := t.renewTokenOnce
	if t.kubernetes != nil {
		refresh = t.login
	}
	for t.renews(ctx, lease) {
		delay := t.renewalDelay(lease.ttl)
		for {
			if !t.waitToRefresh(ctx, delay) {
				return
			}
			next, err := tracedRefresh(ctx, refresh)
			if err == nil {
				lease = next
				break
			}
			slog.ErrorContext(ctx, "failed to refresh the transit token", "error", err, "retryIn", t.renewRetry)
			delay = t.renewRetry
		}
		t.setTokenLease(lease)
	}
}

func (t *Client) renews(ctx context.Context, lease tokenLease) bool {
	if lease.ttl <= 0 {
		slog.InfoContext(ctx, "transit token has no ttl; not renewing it")
		return false
	}
	if t.kubernetes == nil && !lease.renewable {
		slog.WarnContext(ctx, "transit token is not renewable", "expiresAt", t.tokenExpiry())
		return false
	}
	return true
}

func (t *Client) waitToRefresh(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	case <-t.rejected:
		backoff := t.rejectionBackoff()
		slog.WarnContext(ctx, "transit rejected the token; logging in again", "in", backoff)
		if !sleepContext(ctx, backoff) {
			return false
		}
	}
	t.drainRejections()
	return true
}

func tracedRefresh(ctx context.Context, refresh func(context.Context) (tokenLease, error)) (tokenLease, error) {
	ctx, span := tracing.StartRoot(ctx, tokenRefreshSpan)
	lease, err := refresh(ctx)
	tracing.End(span, err)
	return lease, err
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (t *Client) lookupToken(ctx context.Context) (tokenLease, error) {
	var response lookupResponse
	if err := t.do(ctx, http.MethodGet, lookupSelfPath, nil, &response); err != nil {
		return tokenLease{}, fmt.Errorf("look up transit token: %w", err)
	}
	ttl := time.Duration(response.Data.TTL) * time.Second
	return tokenLease{ttl: ttl, renewable: response.Data.Renewable}, nil
}

func (t *Client) renewTokenOnce(ctx context.Context) (tokenLease, error) {
	var response renewResponse
	if err := t.do(ctx, http.MethodPost, renewSelfPath, struct{}{}, &response); err != nil {
		return tokenLease{}, fmt.Errorf("renew transit token: %w", err)
	}
	if response.Auth.LeaseDuration <= 0 {
		return tokenLease{}, ErrNoLease
	}
	ttl := time.Duration(response.Auth.LeaseDuration) * time.Second
	return tokenLease{ttl: ttl, renewable: response.Auth.Renewable}, nil
}
