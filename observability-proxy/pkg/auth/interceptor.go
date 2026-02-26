package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect"
)

type tokenContextKey struct{}

// TokenFromContext extracts the raw token string from the request context.
func TokenFromContext(ctx context.Context) (string, bool) {
	t, ok := ctx.Value(tokenContextKey{}).(string)
	return t, ok && t != ""
}

// extractToken reads the bearer token from Authorization header or loco_token cookie,
// mirroring the pattern used in api/middleware/githubOauth.go.
func extractToken(header http.Header) (string, error) {
	authHeader := header.Get("Authorization")
	if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer "), nil
	}

	cookieHeader := header.Get("Cookie")
	cookies, err := http.ParseCookie(cookieHeader)
	if err != nil {
		return "", err
	}
	for _, c := range cookies {
		if c.Name == "loco_token" {
			return c.Value, nil
		}
	}

	return "", errors.New("no token provided")
}

// AuthInterceptor extracts the token from the request and injects it into context.
// Actual permission checks are performed per-handler via the Validator.
type AuthInterceptor struct{}

func NewAuthInterceptor() *AuthInterceptor {
	return &AuthInterceptor{}
}

func (i *AuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		token, err := extractToken(req.Header())
		if err != nil {
			return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("missing token: %w", err))
		}
		ctx = context.WithValue(ctx, tokenContextKey{}, token)
		return next(ctx, req)
	}
}

func (i *AuthInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *AuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		token, err := extractToken(conn.RequestHeader())
		if err != nil {
			return connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("missing token: %w", err))
		}
		ctx = context.WithValue(ctx, tokenContextKey{}, token)
		return next(ctx, conn)
	}
}
