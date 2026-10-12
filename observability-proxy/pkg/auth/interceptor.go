package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/team-loco/loco/internal/httputil"

	"connectrpc.com/connect/v2"
)

type tokenContextKey struct{}

// TokenFromContext extracts the raw token string from the request context.
func TokenFromContext(ctx context.Context) (string, bool) {
	t, ok := ctx.Value(tokenContextKey{}).(string)
	return t, ok && t != ""
}

var errNoToken = errors.New("no bearer token provided")

// extractToken reads the bearer token from the Authorization header.
func extractToken(header httputil.HeaderGetter) (string, error) {
	authHeader := header.Get("Authorization")
	if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer "), nil
	}

	return "", errNoToken
}

// AuthInterceptor extracts the token from the request and injects it into context.
// Actual permission checks are performed per-handler via the Validator.
type AuthInterceptor struct{}

func NewAuthInterceptor() *AuthInterceptor {
	return &AuthInterceptor{}
}

func (*AuthInterceptor) Interceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			info, _ := connect.CallInfoForServerContext(ctx)
			token, err := extractToken(info.RequestHeader())
			if err != nil {
				return connect.Errorf(connect.CodeUnauthenticated, "missing token: %s", err).WithCause(err)
			}
			ctx = context.WithValue(ctx, tokenContextKey{}, token)
			return next(ctx, spec, stream)
		}
	}
}
