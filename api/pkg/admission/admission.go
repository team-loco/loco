package admission

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	"github.com/team-loco/loco/gen/go/loco/build/v1/buildv1connect"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/gen/go/loco/plan/v1/planv1connect"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
)

type Config struct {
	Window            time.Duration
	GlobalRequests    int32
	PrincipalRequests int32
	WorkspaceRequests int32
	GlobalDeploys     int32
	WorkspaceDeploys  int32
	GlobalBuilds      int32
	WorkspaceBuilds   int32
}

type Limiter struct {
	pool   *pgxpool.Pool
	config Config
}

func New(pool *pgxpool.Pool, config Config) *Limiter {
	return &Limiter{pool: pool, config: config}
}

type budget struct {
	key   string
	limit int32
}

func (l *Limiter) Interceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			budgets := []budget{{key: "requests:global", limit: l.config.GlobalRequests}}
			if p, ok := reqctx.PrincipalFrom(ctx); ok {
				budgets = append(
					budgets,
					budget{
						key:   "requests:principal:" + string(p.Entity.Type) + ":" + p.Entity.ID.String(),
						limit: l.config.PrincipalRequests,
					},
				)
			}
			if err := l.consume(ctx, budgets); err != nil {
				return err
			}
			return next(l.Context(ctx, spec.Procedure), spec, stream)
		}
	}
}

type contextKey struct{}

type request struct {
	limiter    *Limiter
	procedure  string
	mu         sync.Mutex
	workspaces map[uuid.UUID]bool
}

func (l *Limiter) Context(ctx context.Context, procedure string) context.Context {
	return context.WithValue(
		ctx,
		contextKey{},
		&request{limiter: l, procedure: procedure, workspaces: make(map[uuid.UUID]bool)},
	)
}

// Authorize is called only after permission checks, so a caller cannot spend another workspace's budget.
func Authorize(ctx context.Context, required db.EntityScope) error {
	r, ok := ctx.Value(contextKey{}).(*request)
	if !ok {
		return nil
	}
	config := r.limiter.config
	var work string
	var global, workspace int32
	switch r.procedure {
	case deploymentv1connect.DeploymentServiceCreateDeploymentProcedure,
		planv1connect.PlanServiceApplyProcedure,
		resourcev1connect.ResourceServiceScaleResourceProcedure:
		work, global, workspace = "deploys", config.GlobalDeploys, config.WorkspaceDeploys
	case buildv1connect.BuildServiceCreateBuildProcedure:
		work, global, workspace = "builds", config.GlobalBuilds, config.WorkspaceBuilds
	}
	if config.WorkspaceRequests == 0 && global == 0 && workspace == 0 {
		return nil
	}
	var workspaceID uuid.UUID
	switch required.EntityType {
	case db.EntityTypeWorkspace:
		workspaceID = required.EntityID
	case db.EntityTypeResource:
		owner, err := db.New(r.limiter.pool).GetWorkspaceOrganizationIDByResourceID(ctx, required.EntityID)
		if errors.Is(err, pgx.ErrNoRows) {
			// Let the handler preserve its missing-resource response.
			return nil
		}
		if err != nil {
			return exhausted(ctx, int64(config.Window.Seconds())+1).WithCause(err)
		}
		workspaceID = owner.WorkspaceID
	default:
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.workspaces[workspaceID] {
		return nil
	}
	budgets := []budget{{key: "requests:workspace:" + workspaceID.String(), limit: config.WorkspaceRequests}}
	if work != "" {
		if len(r.workspaces) == 0 {
			budgets = append(budgets, budget{key: work + ":global", limit: global})
		}
		budgets = append(budgets, budget{key: work + ":workspace:" + workspaceID.String(), limit: workspace})
	}
	if err := r.limiter.consume(ctx, budgets); err != nil {
		return err
	}
	r.workspaces[workspaceID] = true
	return nil
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
