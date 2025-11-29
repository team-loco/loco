package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/loco-team/loco/api/contextkeys"
	"github.com/loco-team/loco/api/jwtutil"
)

type githubAuthInterceptor struct{}

func NewGithubAuthInterceptor() *githubAuthInterceptor {
	return &githubAuthInterceptor{}
}

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

	for _, cookie := range cookies {
		if cookie.Name == "loco_token" {
			return cookie.Value, nil
		}
	}

	return "", errors.New("no token provided")
}

func (i *githubAuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return connect.UnaryFunc(func(
		ctx context.Context,
		req connect.AnyRequest,
	) (connect.AnyResponse, error) {
		// todo: need to fix the service name
		if req.Spec().Procedure == "/loco.oauth.v1.OAuthService/GithubOAuthDetails" ||
			req.Spec().Procedure == "/loco.oauth.v1.OAuthService/GetGithubAuthorizationURL" ||
			req.Spec().Procedure == "/loco.oauth.v1.OAuthService/ExchangeGithubCode" ||
			req.Spec().Procedure == "/loco.oauth.v1.OAuthService/GetGithubUser" {
			return next(ctx, req)
		}

		token, err := extractToken(req.Header())
		if err != nil {
			slog.Error(err.Error())
			return nil, connect.NewError(connect.CodeUnauthenticated, err)
		}

		claims, err := jwtutil.ValidateLocoJWT(token)
		if err != nil {
			slog.Error(err.Error())
			return nil, connect.NewError(
				connect.CodeUnauthenticated,
				err,
			)
		}

		slog.InfoContext(ctx, "claims validated; populating ctx", slog.Int64("userId", claims.UserId))

		c := context.WithValue(ctx, contextkeys.UserKey, claims.Username)
		c = context.WithValue(c, contextkeys.UserIDKey, claims.UserId)
		c = context.WithValue(c, contextkeys.ExternalUsernameKey, claims.ExternalUsername)

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
		if conn.Spec().Procedure == "/loco.oauth.v1.OAuthService/GithubOAuthDetails" ||
			conn.Spec().Procedure == "/loco.oauth.v1.OAuthService/GetGithubAuthorizationURL" ||
			conn.Spec().Procedure == "/loco.oauth.v1.OAuthService/ExchangeGithubCode" ||
			conn.Spec().Procedure == "/loco.oauth.v1.OAuthService/GetGithubUser" {
			return next(ctx, conn)
		}

		token, err := extractToken(conn.RequestHeader())
		if err != nil {
			slog.Error(err.Error())
			return connect.NewError(connect.CodeUnauthenticated, err)
		}

		claims, err := jwtutil.ValidateLocoJWT(token)
		if err != nil {
			slog.Error(err.Error())
			return connect.NewError(connect.CodeUnauthenticated, err)
		}

		slog.Info("claims validated; populating ctx", slog.Int64("userId", claims.UserId))

		c := context.WithValue(ctx, contextkeys.UserKey, claims.Username)
		c = context.WithValue(c, contextkeys.UserIDKey, claims.UserId)
		c = context.WithValue(c, contextkeys.ExternalUsernameKey, claims.ExternalUsername)

		return next(c, conn)
	})
}
