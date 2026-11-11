package admission

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/cache"
)

type Config struct {
	Window            time.Duration
	AggregateRequests bool
	GlobalRequests    int32
	PrincipalRequests int32
	WorkspaceRequests int32
	GlobalDeploys     int32
	WorkspaceDeploys  int32
	GlobalBuilds      int32
	WorkspaceBuilds   int32
}

type Limiter struct {
	pool          *pgxpool.Pool
	config        Config
	cache         cache.Cache
	requestPrefix string
}

func New(pool *pgxpool.Pool, backend cache.Cache, config Config) *Limiter {
	prefix := "{admission}:requests:"
	if !config.AggregateRequests {
		prefix += uuid.NewString() + ":"
	}
	return &Limiter{pool: pool, cache: backend, config: config, requestPrefix: prefix}
}

type budget struct {
	key   string
	limit int32
}

type contextKey struct{}

func (l *Limiter) Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, l)
}

type Work string

const (
	Deploy Work = "deploys"
	Build  Work = "builds"
)

func AdmitWork(ctx context.Context, workspaceID uuid.UUID, work Work) error {
	l, ok := ctx.Value(contextKey{}).(*Limiter)
	if !ok {
		return nil
	}
	var global, workspace int32
	switch work {
	case Deploy:
		global, workspace = l.config.GlobalDeploys, l.config.WorkspaceDeploys
	case Build:
		global, workspace = l.config.GlobalBuilds, l.config.WorkspaceBuilds
	default:
		panic("invalid admission work kind")
	}
	return l.consume(
		ctx,
		[]budget{
			{key: string(work) + ":global", limit: global},
			{key: string(work) + ":workspace:" + workspaceID.String(), limit: workspace},
		},
	)
}

func (l *Limiter) consume(ctx context.Context, budgets []budget) error {
	enabled := false
	for _, b := range budgets {
		enabled = enabled || b.limit > 0
	}
	if !enabled {
		return nil
	}
	err := pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) error {
		queries := db.New(tx)
		for _, b := range budgets {
			if b.limit == 0 {
				continue
			}
			row, err := queries.ConsumeAdmission(
				ctx,
				db.ConsumeAdmissionParams{Key: b.key, WindowSeconds: l.config.Window.Seconds()},
			)
			if err != nil {
				return fmt.Errorf("consume admission budget: %w", err)
			}
			if row.Used > int64(b.limit) {
				return exhausted(ctx, row.RetrySeconds)
			}
		}
		return nil
	})
	if err == nil {
		return nil
	}
	if connect.CodeOf(err) == connect.CodeResourceExhausted {
		return err
	}
	return exhausted(ctx, int64(l.config.Window.Seconds())+1).WithCause(err)
}

func exhausted(ctx context.Context, seconds int64) *connect.Error {
	delay := strconv.FormatInt(seconds, 10)
	err := connect.NewError(
		connect.CodeResourceExhausted,
		"API admission budget exhausted; retry after "+delay+" seconds",
	)
	if info, ok := connect.CallInfoForServerContext(ctx); ok {
		info.ResponseHeader().Set("Retry-After", delay)
	}
	return err
}
