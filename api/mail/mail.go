package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/url"
	"time"
)

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

var (
	ErrNoTransport = errors.New("no email transport is configured")

	ErrInvalidTransport  = errors.New("EMAIL_TRANSPORT is invalid")
	ErrUnsupportedScheme = errors.New("EMAIL_TRANSPORT must use http:// or https:// (or be unset)")
	ErrHookSecret        = errors.New(
		"EMAIL_HOOK_SECRET is required for an http:// or https:// transport and must be whsec_ plus base64",
	)
)

type Transport interface {
	Send(ctx context.Context, event InvitationEvent) error
}

type Option func(*options)

type options struct {
	tlsConfig *tls.Config
}

func WithTLSConfig(config *tls.Config) Option {
	return func(o *options) { o.tlsConfig = config }
}

func ParseTransport(raw, hookSecret string, timeout time.Duration, opts ...Option) (Transport, error) {
	var applied options
	for _, opt := range opts {
		opt(&applied)
	}
	if raw == "" {
		return none{}, nil
	}
	u, err := url.Parse(raw)
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		return nil, fmt.Errorf("%w: %w", ErrInvalidTransport, urlErr.Err)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidTransport, err)
	}
	switch u.Scheme {
	case schemeHTTP, schemeHTTPS:
		return newHook(u, hookSecret, applied.tlsConfig, timeout)
	}
	return nil, ErrUnsupportedScheme
}
