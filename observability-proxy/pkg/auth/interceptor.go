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

var errNoToken = errors.New("no bearer token provided")

// extractToken reads the bearer token from the Authorization header.
func extractToken(header http.Header) (string, error) {
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

func (*AuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		token, err := extractToken(req.Header())
		if err != nil {
			return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("missing token: %w", err))
		}
		ctx = context.WithValue(ctx, tokenContextKey{}, token)
		return next(ctx, req)
	}
}

func (*AuthInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (*AuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		token, err := extractToken(conn.RequestHeader())
		if err != nil {
			return connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("missing token: %w", err))
		}
		ctx = context.WithValue(ctx, tokenContextKey{}, token)
		return next(ctx, conn)
	}
}
