package secretkeys

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

const (
	transitLookupSelfPath  = "auth/token/lookup-self"
	transitRenewSelfPath   = "auth/token/renew-self"
	shortTokenRenewDivisor = 2
)

var (
	ErrTransitRenewal      = errors.New("transit token renewal margin and retry interval must be positive")
	ErrTransitTokenExpired = errors.New("transit token has expired")
	ErrTransitNoLease      = errors.New("transit token renewal returned no lease")
)

type transitLookupResponse struct {
	Data struct {
		TTL       int64 `json:"ttl"`
		Renewable bool  `json:"renewable"`
	} `json:"data"`
}

type transitRenewResponse struct {
	Auth struct {
		LeaseDuration int64 `json:"lease_duration"`
		Renewable     bool  `json:"renewable"`
	} `json:"auth"`
}

type tokenLease struct {
	ttl       time.Duration
	renewable bool
}

func (t *Transit) tokenExpiry() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.expiresAt
}

func (t *Transit) setTokenExpiry(lease tokenLease) {
	expiry := time.Time{}
	if lease.ttl > 0 {
		expiry = t.now().Add(lease.ttl)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expiresAt = expiry
}

func (t *Transit) checkToken() error {
	expiry := t.tokenExpiry()
	if expiry.IsZero() || t.now().Before(expiry) {
		return nil
	}
	return fmt.Errorf("%w at %s", ErrTransitTokenExpired, expiry.Format(time.RFC3339))
}

func (t *Transit) renewalDelay(ttl time.Duration) time.Duration {
	if ttl > t.renewMargin {
		return ttl - t.renewMargin
	}
	return ttl / shortTokenRenewDivisor
}

// renewToken looks the token up, then renews it renewMargin before each expiry until ctx
// is done. It stops when the token has no TTL or can no longer be renewed. A failed
// lookup or renewal is logged and retried every renewRetry.
func (t *Transit) renewToken(ctx context.Context) {
	refresh := t.lookupToken
	delay := time.Duration(0)
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		lease, err := refresh(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "failed to refresh the transit token", "error", err, "retryIn", t.renewRetry)
			delay = t.renewRetry
			continue
		}
		t.setTokenExpiry(lease)
		if lease.ttl <= 0 {
			slog.InfoContext(ctx, "transit token has no ttl; not renewing it")
			return
		}
		if !lease.renewable {
			slog.WarnContext(ctx, "transit token is not renewable", "expiresAt", t.tokenExpiry())
			return
		}
		refresh = t.renewTokenOnce
		delay = t.renewalDelay(lease.ttl)
	}
}

func (t *Transit) lookupToken(ctx context.Context) (tokenLease, error) {
	var response transitLookupResponse
	if err := t.do(ctx, http.MethodGet, transitLookupSelfPath, nil, &response); err != nil {
		return tokenLease{}, fmt.Errorf("look up transit token: %w", err)
	}
	ttl := time.Duration(response.Data.TTL) * time.Second
	return tokenLease{ttl: ttl, renewable: response.Data.Renewable}, nil
}

func (t *Transit) renewTokenOnce(ctx context.Context) (tokenLease, error) {
	var response transitRenewResponse
	if err := t.do(ctx, http.MethodPost, transitRenewSelfPath, struct{}{}, &response); err != nil {
		return tokenLease{}, fmt.Errorf("renew transit token: %w", err)
	}
	if response.Auth.LeaseDuration <= 0 {
		return tokenLease{}, ErrTransitNoLease
	}
	ttl := time.Duration(response.Auth.LeaseDuration) * time.Second
	return tokenLease{ttl: ttl, renewable: response.Auth.Renewable}, nil
}
