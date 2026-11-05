package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/auth"
	genDb "github.com/team-loco/loco/api/gen/db"
	bootstrapv1 "github.com/team-loco/loco/gen/go/loco/bootstrap/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrBootstrapDisabled     = errors.New("bootstrap is not enabled on this installation")
	ErrBootstrapTokenInvalid = errors.New("invalid bootstrap token")
	ErrBootstrapClosed       = errors.New("this installation already has a system admin")
	ErrBootstrapUserRequired = errors.New("bootstrap requires a verified identity provider sign-in")
	errBootstrap             = errors.New("bootstrap failed")
)

func HashBootstrapToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

type BootstrapServer struct {
	pool      *pgxpool.Pool
	tokenHash []byte
	verifier  *auth.Verifier
	resolver  *auth.Resolver
	sessions  *AuthServer
}

func NewBootstrapServer(
	pool *pgxpool.Pool,
	tokenHash []byte,
	verifier *auth.Verifier,
	resolver *auth.Resolver,
	sessions *AuthServer,
) *BootstrapServer {
	return &BootstrapServer{
		pool:      pool,
		tokenHash: tokenHash,
		verifier:  verifier,
		resolver:  resolver,
		sessions:  sessions,
	}
}

func (s *BootstrapServer) Bootstrap(
	ctx context.Context,
	req *bootstrapv1.BootstrapRequest,
) (*bootstrapv1.BootstrapResponse, error) {
	if s.tokenHash == nil {
		return nil, rpcerr.New(connect.CodeFailedPrecondition, ErrBootstrapDisabled)
	}
	presented := HashBootstrapToken(req.GetToken())
	if subtle.ConstantTimeCompare(presented, s.tokenHash) != 1 {
		return nil, rpcerr.New(connect.CodePermissionDenied, ErrBootstrapTokenInvalid)
	}
	info, _ := connect.CallInfoForServerContext(ctx)
	raw, found := strings.CutPrefix(info.RequestHeader().Get("Authorization"), "Bearer ")
	if !found || !s.verifier.Enabled() {
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrBootstrapUserRequired)
	}
	identity, err := s.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrBootstrapUserRequired).WithCause(err)
	}
	var grant cliGrant
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		queries := genDb.New(tx)
		// Insert before creating the user: the singleton serializes contenders and
		// rolls back with every identity mutation if this attempt cannot complete.
		closed, closeErr := queries.CloseSystemBootstrap(ctx, nil)
		if closeErr != nil {
			return closeErr
		}
		if closed == 0 {
			return ErrBootstrapClosed
		}
		exists, existsErr := queries.SystemAdminExists(ctx)
		if existsErr != nil {
			return existsErr
		}
		if exists {
			return ErrBootstrapClosed
		}
		user, stored, resolveErr := s.resolver.BootstrapUser(ctx, queries, identity)
		if resolveErr != nil {
			return resolveErr
		}
		if scopeErr := queries.AddUserScope(ctx, genDb.AddUserScopeParams{
			UserID: user.ID, Scope: genDb.ScopeAdmin, EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil,
		}); scopeErr != nil {
			return scopeErr
		}
		if ownerErr := queries.SetSystemBootstrapOwner(ctx, &user.ID); ownerErr != nil {
			return ownerErr
		}
		grant = cliGrant{UserID: user.ID, IdentityID: stored.ID}
		return nil
	})
	if errors.Is(err, ErrBootstrapClosed) {
		return nil, rpcerr.New(connect.CodeAlreadyExists, ErrBootstrapClosed)
	}
	if rejected, ok := errors.AsType[*auth.SignupRejectedError](err); ok {
		return nil, rpcerr.New(connect.CodePermissionDenied, rejected)
	}
	if errors.Is(err, auth.ErrBootstrapIdentityInactive) {
		return nil, rpcerr.New(connect.CodePermissionDenied, auth.ErrBootstrapIdentityInactive)
	}
	if err != nil {
		slog.ErrorContext(ctx, "bootstrap failed", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, errBootstrap)
	}
	tokens, err := s.sessions.issue(ctx, grant, info)
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "bootstrap granted system admin", "user_id", grant.UserID)
	return &bootstrapv1.BootstrapResponse{UserId: grant.UserID.String(), Tokens: tokens}, nil
}
