package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	netmail "net/mail"
	"net/url"
	"time"

	"github.com/team-loco/loco/api/webhooks"
)

const (
	schemeSMTP  = "smtp"
	schemeHTTPS = "https"
)

var (
	ErrNoTransport = errors.New("no email transport is configured")

	ErrFromRequired      = errors.New("EMAIL_FROM is required when EMAIL_TRANSPORT is set")
	ErrInvalidFrom       = errors.New("EMAIL_FROM is not a valid address")
	ErrInvalidTransport  = errors.New("EMAIL_TRANSPORT is invalid")
	ErrUnsupportedScheme = errors.New("EMAIL_TRANSPORT must be smtp://user:pass@host:port or https://")
	ErrHookSecret        = errors.New(
		"EMAIL_HOOK_SECRET is required for an https:// transport and must be whsec_ plus base64",
	)
	ErrSMTPNoSTARTTLS = errors.New("smtp server does not offer STARTTLS")
)

type Message struct {
	To      string
	ReplyTo string
	Subject string
	Body    string
	Event   any
}

type Transport interface {
	Send(ctx context.Context, msg Message) error
}

type Option func(*options)

type options struct {
	tlsConfig *tls.Config
}

func WithTLSConfig(config *tls.Config) Option {
	return func(o *options) { o.tlsConfig = config }
}

func ParseTransport(raw, from, hookSecret string, opts ...Option) (Transport, error) {
	var applied options
	for _, opt := range opts {
		opt(&applied)
	}
	if raw == "" {
		return none{}, nil
	}
	if from == "" {
		return nil, ErrFromRequired
	}
	sender, err := netmail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidFrom, err)
	}
	u, err := url.Parse(raw)
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		return nil, fmt.Errorf("%w: %w", ErrInvalidTransport, urlErr.Err)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidTransport, err)
	}
	switch u.Scheme {
	case schemeSMTP:
		return newSMTP(u, sender, applied.tlsConfig)
	case schemeHTTPS:
		return newHook(u, hookSecret, applied.tlsConfig)
	}
	return nil, ErrUnsupportedScheme
}

func validHookSecret(secret string) bool {
	_, err := webhooks.Sign(secret, "", time.Time{}, nil)
	return err == nil
}
