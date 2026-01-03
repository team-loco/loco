package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"

	"github.com/team-loco/loco/api/tvm"
)

// TODO: repeated code !!

type githubAuthInterceptor struct {
	machine *tvm.VendingMachine
}

func NewGithubAuthInterceptor(machine *tvm.VendingMachine) *githubAuthInterceptor {
	return &githubAuthInterceptor{
		machine: machine,
	}
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
			req.Spec().Procedure == "/loco.oauth.v1.OAuthService/ExchangeGithubToken" ||
			req.Spec().Procedure == "/loco.oauth.v1.OAuthService/GetGithubUser" {
			return next(ctx, req)
		}

		token, err := extractToken(req.Header())
		if err != nil {
			slog.Error(err.Error())
			return nil, connect.NewError(connect.CodeUnauthenticated, err)
		}

		entity, scopes, err := i.machine.GetToken(ctx, token)
		if err != nil {
			slog.Error(err.Error())
			return nil, connect.NewError(connect.CodeUnauthenticated, err)
		}

		c := context.WithValue(ctx, contextkeys.EntityKey, genDb.Entity{
			Type: entity.Type,
			ID:   entity.ID,
		})
		c = context.WithValue(c, contextkeys.EntityScopesKey, scopes)

		slog.InfoContext(c, "claims validated; populating ctx", slog.Int64("userId", entity.ID))

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
			conn.Spec().Procedure == "/loco.oauth.v1.OAuthService/ExchangeGithubToken" ||
			conn.Spec().Procedure == "/loco.oauth.v1.OAuthService/GetGithubUser" {
			return next(ctx, conn)
		}

		token, err := extractToken(conn.RequestHeader())
		if err != nil {
			slog.Error(err.Error())
			return connect.NewError(connect.CodeUnauthenticated, err)
		}

		entity, scopes, err := i.machine.GetToken(ctx, token)
		if err != nil {
			slog.Error(err.Error())
			return connect.NewError(connect.CodeUnauthenticated, err)
		}

		slog.InfoContext(ctx, "claims validated; populating ctx", slog.Int64("entityId", entity.ID), slog.Any("entityType", entity.Type))

		c := context.WithValue(ctx, contextkeys.EntityKey, genDb.Entity{
			Type: entity.Type,
			ID:   entity.ID,
		})
		c = context.WithValue(ctx, contextkeys.EntityScopesKey, scopes)

		return next(c, conn)
	})
}
