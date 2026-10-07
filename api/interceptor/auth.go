package interceptor

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"

	"github.com/team-loco/loco/api/tvm"
)

var errNoToken = errors.New("no bearer token provided")

type authInterceptor struct {
	machine  *tvm.VendingMachine
	verifier *auth.Verifier
	resolver *auth.Resolver
	gate     *auth.SSOGate
	authz    *authz.Authorizer
}

func NewAuthInterceptor(
	machine *tvm.VendingMachine,
	verifier *auth.Verifier,
	resolver *auth.Resolver,
	gate *auth.SSOGate,
	authorizer *authz.Authorizer,
) *authInterceptor {
	return &authInterceptor{
		machine:  machine,
		verifier: verifier,
		resolver: resolver,
		gate:     gate,
		authz:    authorizer,
	}
}

func (i *authInterceptor) withCaller(
	ctx context.Context,
	entity genDb.Entity,
	scopes []genDb.EntityScope,
	connection *string,
	token string,
) (context.Context, error) {
	allowed, err := i.gate.Filter(ctx, scopes, connection)
	if err != nil {
		slog.ErrorContext(ctx, "failed to apply sso requirements", "error", err)
		return nil, connect.NewError(connect.CodeInternal, auth.ErrResolve)
	}
	c := context.WithValue(ctx, contextkeys.EntityKey, entity)
	c = context.WithValue(c, contextkeys.EntityScopesKey, allowed)
	c = context.WithValue(c, contextkeys.TokenKey, token)
	if connection != nil {
		c = context.WithValue(c, contextkeys.SSOConnectionKey, *connection)
	}
	return c, nil
}

func extractToken(header http.Header) (string, error) {
	authHeader := header.Get("Authorization")
	if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer "), nil
	}

	return "", errNoToken
}

var publicProcedures = map[string]struct{}{
	authv1connect.AuthServiceExchangeCLICodeProcedure:  {},
	authv1connect.AuthServiceStartDeviceLoginProcedure: {},
	authv1connect.AuthServicePollDeviceLoginProcedure:  {},
	authv1connect.AuthServiceRefreshCLITokenProcedure:  {},
}

func isPublicProcedure(procedure string) bool {
	_, ok := publicProcedures[procedure]
	return ok
}

func (i *authInterceptor) authenticate(
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

	if auth.LooksLikeJWT(token) && i.verifier.Enabled() {
		return i.authenticateProviderToken(ctx, procedure, token)
	}

	caller, err := i.machine.Authenticate(ctx, token)
	if err != nil {
		slog.WarnContext(ctx, "token rejected", "procedure", procedure, "error", err)
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	return i.withCaller(ctx, caller.Entity, caller.Scopes, caller.SSOConnection, token)
}

func (i *authInterceptor) authenticateProviderToken(
	ctx context.Context,
	procedure string,
	token string,
) (context.Context, error) {
	identity, err := i.verifier.Verify(ctx, token)
	if err != nil {
		slog.WarnContext(ctx, "provider token rejected", "procedure", procedure, "error", err)
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	user, err := i.resolver.Resolve(ctx, identity)
	if err != nil {
		if rejected, ok := errors.AsType[*auth.SignupRejectedError](err); ok {
			slog.InfoContext(ctx, "sign-in rejected", "issuer", identity.Issuer, "reason", rejected.Message)
			return nil, connect.NewError(connect.CodePermissionDenied, rejected)
		}
		slog.ErrorContext(ctx, "failed to resolve identity", "issuer", identity.Issuer, "error", err)
		return nil, connect.NewError(connect.CodeInternal, auth.ErrResolve)
	}

	scopes, err := i.authz.UserScopes(ctx, user.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to load user scopes", "userId", user.ID, "error", err)
		return nil, connect.NewError(connect.CodeInternal, auth.ErrResolve)
	}

	entity := genDb.Entity{Type: genDb.EntityTypeUser, ID: user.ID}
	c, err := i.withCaller(ctx, entity, scopes, identity.SSOConnection, token)
	if err != nil {
		return nil, err
	}
	return context.WithValue(c, contextkeys.IdentityKey, identity), nil
}

func (i *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
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

func (*authInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return connect.StreamingClientFunc(func(
		ctx context.Context,
		spec connect.Spec,
	) connect.StreamingClientConn {
		conn := next(ctx, spec)
		return conn
	})
}

func (i *authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
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
