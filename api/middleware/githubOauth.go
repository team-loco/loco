package middleware

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	"github.com/loco-team/loco/api/jwtutil"
)

type githubAuthInterceptor struct{}

func NewGithubAuthInterceptor() *githubAuthInterceptor {
	return &githubAuthInterceptor{}
}

func (i *githubAuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return connect.UnaryFunc(func(
		ctx context.Context,
		req connect.AnyRequest,
	) (connect.AnyResponse, error) {
		// todo: need to fix the service name
		if req.Spec().Procedure == "/shared.proto.oauth.v1.OAuthService/GithubOAuthDetails" ||
			req.Spec().Procedure == "/shared.proto.oauth.v1.OAuthService/ExchangeGithubToken" {
			return next(ctx, req)
		}

		authHeader := req.Header().Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			return nil, connect.NewError(
				connect.CodeUnauthenticated,
				errors.New("no token provided"),
			)
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")

		claims, err := jwtutil.ValidateLocoJWT(token)
		if err != nil {
			slog.Error(err.Error())
			return nil, connect.NewError(
				connect.CodeUnauthenticated,
				err,
			)
		}

		slog.Info("claims validated; populating ctx", slog.Int64("userId", claims.UserId))

		c := context.WithValue(ctx, "user", claims.Username)
		c = context.WithValue(c, "userId", claims.UserId)
		c = context.WithValue(c, "externalUsername", claims.ExternalUsername)

		return next(c, req)
	})
}

func (i *githubAuthInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return connect.StreamingClientFunc(func(
		ctx context.Context,
		spec connect.Spec,
	) connect.StreamingClientConn {
		conn := next(ctx, spec)
		return conn
	})
}

// todo: logic is very similar to unary; should refactor this
func (i *githubAuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return connect.StreamingHandlerFunc(func(
		ctx context.Context,
		conn connect.StreamingHandlerConn,
	) error {
		if conn.Spec().Procedure == "/shared.proto.oauth.v1.OAuthService/GithubOAuthDetails" ||
			conn.Spec().Procedure == "/shared.proto.oauth.v1.OAuthService/ExchangeGithubToken" {
			return next(ctx, conn)
		}
		authHeader := conn.RequestHeader().Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			return connect.NewError(
				connect.CodeUnauthenticated,
				errors.New("no token provided"),
			)
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")

		claims, err := jwtutil.ValidateLocoJWT(token)
		if err != nil {
			slog.Error(err.Error())
			return connect.NewError(
				connect.CodeUnauthenticated,
				err,
			)
		}

		slog.Info("claims validated; populating ctx", slog.Int64("userId", claims.UserId))

		c := context.WithValue(ctx, "user", claims.Username)
		c = context.WithValue(c, "userId", claims.UserId)
		c = context.WithValue(c, "externalUsername", claims.ExternalUsername)

		return next(c, conn)
	})
}
