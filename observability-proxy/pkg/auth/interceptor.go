package auth

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"

	"github.com/team-loco/loco/internal/bearer"
)

type tokenContextKey struct{}

// TokenFromContext extracts the raw token string from the request context.
func TokenFromContext(ctx context.Context) (string, bool) {
	t, ok := ctx.Value(tokenContextKey{}).(string)
	return t, ok && t != ""
}

var errNoToken = errors.New("no bearer token provided")

// Interceptor extracts the token from the request and injects it into context.
// Actual permission checks are performed per-handler via the Validator.
func Interceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			info, ok := connect.CallInfoForServerContext(ctx)
			if !ok {
				return connect.Errorf(connect.CodeUnauthenticated, "missing token").WithCause(errNoToken)
			}
			token, ok := bearer.FromHeader(info.RequestHeader())
			if !ok {
				return connect.Errorf(connect.CodeUnauthenticated, "missing token").WithCause(errNoToken)
			}
			ctx = context.WithValue(ctx, tokenContextKey{}, token)
			return next(ctx, spec, stream)
		}
	}
}
