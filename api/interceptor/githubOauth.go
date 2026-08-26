package interceptor

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/gen/go/loco/oauth/v1/oauthv1connect"

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
		if req.Spec().Procedure == oauthv1connect.OAuthServiceGetOAuthDetailsProcedure ||
			req.Spec().Procedure == oauthv1connect.OAuthServiceGetOAuthAuthorizationURLProcedure ||
			req.Spec().Procedure == oauthv1connect.OAuthServiceExchangeOAuthCodeProcedure ||
			req.Spec().Procedure == oauthv1connect.OAuthServiceExchangeOAuthTokenProcedure ||
			req.Spec().Procedure == oauthv1connect.OAuthServiceRefreshTokenProcedure {
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
		c = context.WithValue(c, contextkeys.TokenKey, token)

		slog.InfoContext(c, "claims validated; populating ctx", "userId", entity.ID.String())

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
		if conn.Spec().Procedure == oauthv1connect.OAuthServiceGetOAuthDetailsProcedure ||
			conn.Spec().Procedure == oauthv1connect.OAuthServiceGetOAuthAuthorizationURLProcedure ||
			conn.Spec().Procedure == oauthv1connect.OAuthServiceExchangeOAuthCodeProcedure ||
			conn.Spec().Procedure == oauthv1connect.OAuthServiceExchangeOAuthTokenProcedure ||
			conn.Spec().Procedure == oauthv1connect.OAuthServiceRefreshTokenProcedure {
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

		slog.InfoContext(ctx, "claims validated; populating ctx", "entityId", entity.ID.String(), "entityType", entity.Type)

		c := context.WithValue(ctx, contextkeys.EntityKey, genDb.Entity{
			Type: entity.Type,
			ID:   entity.ID,
		})
		c = context.WithValue(c, contextkeys.EntityScopesKey, scopes)
		c = context.WithValue(c, contextkeys.TokenKey, token)

		return next(c, conn)
	})
}
