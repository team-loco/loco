package auth

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/team-loco/loco/api/reqctx"
)

var errStreamValidation = errors.New("stream credential validation is not configured")

// StreamConfig bounds how long an open stream can retain outdated access.
type StreamConfig struct {
	Interval time.Duration
	Timeout  time.Duration
}

type streamValidation struct {
	config StreamConfig
	read   func(context.Context) (reqctx.Principal, error)
}

type streamValidationKey struct{}

// WithStreamValidation supplies the authenticated credential's read-only validator.
func WithStreamValidation(
	ctx context.Context,
	config StreamConfig,
	read func(context.Context) (reqctx.Principal, error),
) context.Context {
	return context.WithValue(ctx, streamValidationKey{}, streamValidation{config: config, read: read})
}

// GuardStream rechecks the original credential and the stream's target permissions,
// including while no data is sent. Stop joins the validator and returns its failure.
func GuardStream(
	ctx context.Context,
	authorize func(context.Context) error,
) (context.Context, func() error, error) {
	validation, ok := ctx.Value(streamValidationKey{}).(streamValidation)
	if !ok || validation.read == nil || validation.config.Interval <= 0 || validation.config.Timeout <= 0 {
		return nil, nil, errStreamValidation
	}
	check := func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, validation.config.Timeout)
		defer cancel()
		principal, err := validation.read(ctx)
		if err == nil {
			err = authorize(reqctx.WithPrincipal(ctx, principal))
		}
		// A dependency can cancel its own work while this request remains live.
		if errors.Is(err, context.Canceled) && ctx.Err() == nil {
			return connect.NewError(connect.CodeInternal, "stream validation failed").WithCause(err)
		}
		return err
	}
	if err := check(ctx); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	done := make(chan struct{})
	var failure error
	go func() {
		ticker := time.NewTicker(validation.config.Interval)
		defer close(done)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := check(ctx); err != nil {
					if ctx.Err() != nil {
						return
					}
					cancel(err)
					failure = err
					if abort, ok := ctx.Value(streamAbortKey{}).(func(time.Duration)); ok {
						abort(validation.config.Timeout)
					}
					return
				}
			}
		}
	}()
	stop := func() error {
		cancel(nil)
		<-done
		return failure
	}
	return ctx, stop, nil
}
