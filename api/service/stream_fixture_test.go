package service

import (
	"context"
	"time"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/reqctx"
)

// Stream behavior fixtures supply principals directly instead of authenticating
// credentials. Credential-lifetime tests use the real auth interceptor.
func streamTestContext(ctx context.Context, principal reqctx.Principal) context.Context {
	ctx = reqctx.WithPrincipal(ctx, principal)
	return auth.WithStreamValidation(ctx, auth.StreamConfig{Interval: time.Minute, Timeout: time.Second},
		func(context.Context) (reqctx.Principal, error) { return principal, nil })
}
