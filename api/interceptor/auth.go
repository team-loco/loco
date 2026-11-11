package interceptor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect/v2"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/authz"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	errNoToken       = errors.New("no bearer token provided")
	errUserSuspended = errors.New("user is suspended")
)

type authInterceptor struct {
	machine  *tvm.VendingMachine
	verifier *auth.Verifier
	resolver *auth.Resolver
	authz    *authz.Authorizer
}

func NewAuthInterceptor(
	machine *tvm.VendingMachine,
	verifier *auth.Verifier,
	resolver *auth.Resolver,
	authorizer *authz.Authorizer,
) *authInterceptor {
	return &authInterceptor{
		machine:  machine,
		verifier: verifier,
		resolver: resolver,
		authz:    authorizer,
	}
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
	header bearer.HeaderGetter,
) (context.Context, error) {
	if isPublicProcedure(procedure) {
		return ctx, nil
	}

	token, ok := bearer.FromHeader(header)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(errNoToken)
	}

	if auth.LooksLikeJWT(token) && i.verifier.Enabled() {
		return i.authenticateProviderToken(ctx, procedure, token)
	}

	principal, err := i.machine.GetToken(ctx, token)
	if errors.Is(err, tvm.ErrInvalidExpiredToken) {
		return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
	}
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}

	c := reqctx.WithPrincipal(ctx, principal)

	slog.DebugContext(
		c,
		"claims validated; populating ctx",
		"entityId",
		principal.Entity.ID.String(),
		"entityType",
		principal.Entity.Type,
	)

	return c, nil
}

func (i *authInterceptor) authenticateProviderToken(
	ctx context.Context,
	procedure string,
	token string,
) (context.Context, error) {
	identity, err := i.verifier.Verify(ctx, token)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
	}

	user, claimed, err := i.resolver.Resolve(ctx, identity)
	if err != nil {
		if rejected, ok := errors.AsType[*auth.SignupRejectedError](err); ok {
			slog.InfoContext(ctx, "sign-in rejected", "issuer", identity.Issuer, "reason", rejected.Message)
			return nil, rpcerr.New(connect.CodePermissionDenied, rejected)
		}
		slog.ErrorContext(ctx, "failed to resolve identity", "issuer", identity.Issuer, "error", err)
		return nil, connect.NewError(connect.CodeInternal, auth.ErrResolve.Error()).WithCause(err)
	}
	if user.Status != genDb.UserStatusActive {
		slog.InfoContext(ctx, "suspended user rejected", "procedure", procedure, "userId", user.ID)
		return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(errUserSuspended)
	}

	scopes, err := i.authz.UserScopes(ctx, user.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to load user scopes", "userId", user.ID, "error", err)
		return nil, connect.NewError(connect.CodeInternal, auth.ErrResolve.Error()).WithCause(err)
	}

	c := reqctx.WithPrincipal(ctx, reqctx.Principal{
		Entity:    genDb.Entity{Type: genDb.EntityTypeUser, ID: user.ID},
		Scopes:    scopes,
		Token:     token,
		ActorType: string(genDb.EntityTypeUser),
		ActorID:   user.ID,
		Method:    genDb.DeploymentMethodWeb,
	})
	return auth.WithLogin(c, auth.Login{Identity: identity, ClaimedOrgs: claimed}), nil
}

func (i *authInterceptor) Interceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			info, _ := connect.CallInfoForServerContext(ctx)
			authenticated, err := i.authenticate(ctx, spec.Procedure, info.RequestHeader())
			if err != nil {
				return err
			}
			return next(authenticated, spec, stream)
		}
	}
}
