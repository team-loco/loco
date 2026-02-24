package auth

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
)

type contextKey string

const ClaimsContextKey contextKey = "observability_claims"

// ClaimsFromContext extracts ValidatedClaims from the request context.
func ClaimsFromContext(ctx context.Context) (*ValidatedClaims, bool) {
	claims, ok := ctx.Value(ClaimsContextKey).(*ValidatedClaims)
	return claims, ok
}

// AuthInterceptor validates Bearer tokens and injects claims into context.
// Implements connect.Interceptor to handle both unary and streaming RPCs.
type AuthInterceptor struct {
	validator *Validator
}

func NewAuthInterceptor(validator *Validator) *AuthInterceptor {
	return &AuthInterceptor{validator: validator}
}

func (i *AuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		claims, err := extractAndValidate(ctx, req.Header().Get("Authorization"), i.validator)
		if err != nil {
			return nil, err
		}
		ctx = context.WithValue(ctx, ClaimsContextKey, claims)
		return next(ctx, req)
	}
}

func (i *AuthInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next // proxy is server-side only
}

func (i *AuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		claims, err := extractAndValidate(ctx, conn.RequestHeader().Get("Authorization"), i.validator)
		if err != nil {
			return err
		}
		ctx = context.WithValue(ctx, ClaimsContextKey, claims)
		return next(ctx, conn)
	}
}

func extractAndValidate(ctx context.Context, authHeader string, validator *Validator) (*ValidatedClaims, error) {
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" || token == authHeader {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("missing or invalid authorization header"))
	}
	return validator.Validate(ctx, token)
}
