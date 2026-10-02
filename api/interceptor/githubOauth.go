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

var publicProcedures = map[string]struct{}{
	oauthv1connect.OAuthServiceGetOAuthDetailsProcedure:          {},
	oauthv1connect.OAuthServiceGetOAuthAuthorizationURLProcedure: {},
	oauthv1connect.OAuthServiceExchangeOAuthCodeProcedure:        {},
	oauthv1connect.OAuthServiceExchangeOAuthTokenProcedure:       {},
	oauthv1connect.OAuthServiceRefreshTokenProcedure:             {},
}

func isPublicProcedure(procedure string) bool {
	_, ok := publicProcedures[procedure]
	return ok
}

func (i *githubAuthInterceptor) authenticate(
	ctx context.Context,
	procedure string,
	header http.Header,
) (context.Context, error) {
	if isPublicProcedure(procedure) {
		return ctx, nil
	}

	token, err := extractToken(header)
	if err != nil {
		slog.WarnContext(ctx, "request without a usable token", "procedure", procedure, "error", err)
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	entity, scopes, err := i.machine.GetToken(ctx, token)
	if err != nil {
		slog.WarnContext(ctx, "token rejected", "procedure", procedure, "error", err)
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	c := context.WithValue(ctx, contextkeys.EntityKey, genDb.Entity{
		Type: entity.Type,
		ID:   entity.ID,
	})
	c = context.WithValue(c, contextkeys.EntityScopesKey, scopes)
	c = context.WithValue(c, contextkeys.TokenKey, token)

	slog.DebugContext(
		c,
		"claims validated; populating ctx",
		"entityId",
		entity.ID.String(),
		"entityType",
		entity.Type,
	)

	return c, nil
}

func (i *githubAuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return connect.UnaryFunc(func(
		ctx context.Context,
		req connect.AnyRequest,
	) (connect.AnyResponse, error) {
		c, err := i.authenticate(ctx, req.Spec().Procedure, req.Header())
		if err != nil {
			return nil, err
		}
		return next(c, req)
	})
}

func (*githubAuthInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return connect.StreamingClientFunc(func(
		ctx context.Context,
		spec connect.Spec,
	) connect.StreamingClientConn {
		conn := next(ctx, spec)
		return conn
	})
}

func (i *githubAuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return connect.StreamingHandlerFunc(func(
		ctx context.Context,
		conn connect.StreamingHandlerConn,
	) error {
		c, err := i.authenticate(ctx, conn.Spec().Procedure, conn.RequestHeader())
		if err != nil {
			return err
		}
		return next(c, conn)
	})
}
