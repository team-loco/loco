package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	bootstrapv1 "github.com/team-loco/loco/gen/go/loco/bootstrap/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrBootstrapDisabled     = errors.New("bootstrap is not enabled on this installation")
	ErrBootstrapTokenInvalid = errors.New("invalid bootstrap token")
	ErrBootstrapClosed       = errors.New("this installation already has a system admin")
	ErrBootstrapUserRequired = errors.New("bootstrap requires a signed-in user")

	errBootstrap = errors.New("bootstrap failed")
)

func HashBootstrapToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

type BootstrapServer struct {
	pool      *pgxpool.Pool
	tokenHash []byte
}

func NewBootstrapServer(pool *pgxpool.Pool, tokenHash []byte) *BootstrapServer {
	return &BootstrapServer{pool: pool, tokenHash: tokenHash}
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
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok || entity.Type != genDb.EntityTypeUser {
		return nil, rpcerr.New(connect.CodePermissionDenied, ErrBootstrapUserRequired)
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return bootstrapAdmin(ctx, genDb.New(tx), entity.ID)
	})
	if errors.Is(err, ErrBootstrapClosed) {
		return nil, rpcerr.New(connect.CodeAlreadyExists, ErrBootstrapClosed)
	}
	if err != nil {
		slog.ErrorContext(ctx, "bootstrap failed", "user_id", entity.ID, "error", err)
		return nil, rpcerr.New(connect.CodeInternal, errBootstrap)
	}
	slog.InfoContext(ctx, "bootstrap granted system admin", "user_id", entity.ID)
	return &bootstrapv1.BootstrapResponse{UserId: entity.ID.String()}, nil
}

func bootstrapAdmin(ctx context.Context, queries *genDb.Queries, userID uuid.UUID) error {
	closed, err := queries.CloseSystemBootstrap(ctx, &userID)
	if err != nil {
		return err
	}
	if closed == 0 {
		return ErrBootstrapClosed
	}
	exists, err := queries.SystemAdminExists(ctx)
	if err != nil {
		return err
	}
	if exists {
		return ErrBootstrapClosed
	}
	return queries.AddUserScope(ctx, genDb.AddUserScopeParams{
		UserID:     userID,
		Scope:      genDb.ScopeAdmin,
		EntityType: genDb.EntityTypeSystem,
		EntityID:   uuid.Nil,
	})
}
