package interceptor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/authz"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
	"github.com/team-loco/loco/internal/bearer"
)

var (
	errNoToken       = errors.New("no authentication token provided")
	errWebCookie     = errors.New("web session cookie rejected for this request")
	errUserSuspended = errors.New("user is suspended")
)

type authInterceptor struct {
	machine  *tvm.VendingMachine
	verifier *auth.Verifier
	resolver *auth.Resolver
	authz    *authz.Authorizer
	streams  auth.StreamConfig
	cookies  auth.WebCookies
}

func NewAuthInterceptor(
	machine *tvm.VendingMachine,
	verifier *auth.Verifier,
	resolver *auth.Resolver,
	authorizer *authz.Authorizer,
	streams auth.StreamConfig,
) *authInterceptor {
	return &authInterceptor{
		machine:  machine,
		verifier: verifier,
		resolver: resolver,
		authz:    authorizer,
		streams:  streams,
	}
}

// WebCookies enables dashboard cookie authentication on this interceptor.
func (i *authInterceptor) WebCookies(cookies auth.WebCookies) *authInterceptor {
	i.cookies = cookies
	return i
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
	header interface {
		Get(string) string
		Values(string) []string
	},
) (context.Context, error) {
	if isPublicProcedure(procedure) {
		return ctx, nil
	}

	var token string
	fromCookie := len(header.Values("Authorization")) == 0
	if fromCookie {
		token = i.cookies.Read(header, auth.WebSessionCookie)
		if token != "" && !i.cookies.AllowsRequest(ctx, header) {
			return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(errWebCookie)
		}
	} else {
		token, _ = bearer.FromHeader(header)
	}
	if token == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(errNoToken)
	}

	if !fromCookie && auth.LooksLikeJWT(token) && i.verifier.Enabled() {
		return i.authenticateProviderToken(ctx, procedure, token)
	}

	principal, err := i.machine.GetToken(ctx, token)
	if errors.Is(err, tvm.ErrInvalidExpiredToken) {
		return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
	}
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}

	if fromCookie && principal.Method != genDb.DeploymentMethodWeb {
		return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(errWebCookie)
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
		if errors.Is(err, auth.ErrAccountUnavailable) {
			return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
		}
		if rejected, ok := errors.AsType[*auth.SignupRejectedError](err); ok {
			slog.InfoContext(ctx, "sign-in rejected", "issuer", identity.Issuer, "reason", rejected.Message)
			return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
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
		ActorType: genDb.DeploymentActorTypeUser,
		ActorID:   user.ID,
		UserID:    user.ID,
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
			if original, ok := reqctx.PrincipalFrom(authenticated); ok {
				authenticated = auth.WithStreamValidation(
					authenticated, i.streams, func(ctx context.Context) (reqctx.Principal, error) {
						return i.revalidate(ctx, original)
					},
				)
			}
			return next(authenticated, spec, stream)
		}
	}
}

func (i *authInterceptor) revalidate(ctx context.Context, original reqctx.Principal) (reqctx.Principal, error) {
	if login, provider := auth.LoginFrom(ctx); provider {
		identity, err := i.verifier.Verify(ctx, original.Token)
		if err != nil {
			return reqctx.Principal{}, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
		}
		if identity.Issuer != login.Identity.Issuer || identity.Subject != login.Identity.Subject {
			return reqctx.Principal{}, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized)
		}
		user, err := i.resolver.LookupExisting(ctx, identity)
		if errors.Is(err, pgx.ErrNoRows) {
			return reqctx.Principal{}, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
		}
		if err != nil {
			return reqctx.Principal{}, fmt.Errorf("validate stream user: %w", err)
		}
		if user.ID != original.UserID || user.Status != genDb.UserStatusActive {
			return reqctx.Principal{}, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized)
		}
		original.Scopes, err = i.authz.UserScopes(ctx, original.UserID)
		if err != nil {
			return reqctx.Principal{}, fmt.Errorf("read stream scopes: %w", err)
		}
		return original, nil
	}
	current, err := i.machine.ValidateToken(ctx, original.Token)
	if errors.Is(err, tvm.ErrInvalidExpiredToken) {
		return reqctx.Principal{}, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
	}
	if err != nil {
		return reqctx.Principal{}, fmt.Errorf("validate stream credential: %w", err)
	}
	if current.ActorType != original.ActorType || current.ActorID != original.ActorID ||
		current.UserID != original.UserID || current.Entity != original.Entity ||
		current.Method != original.Method || current.IdentityID != original.IdentityID {
		return reqctx.Principal{}, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized)
	}
	return current, nil
}
