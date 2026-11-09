package sweepers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/advisorylock"
	"github.com/team-loco/loco/api/pkg/tracing"
)

const (
	registrySweepSpan      = "registry.sweep"
	credentialsDeletedAttr = "loco.sweep.registry_credentials_deleted"
)

type RegistrySweepResult struct {
	Ran                bool
	CredentialsDeleted int64
}

type RegistrySweeper struct {
	db       *pgxpool.Pool
	interval time.Duration
}

func NewRegistrySweeper(db *pgxpool.Pool, interval time.Duration) *RegistrySweeper {
	return &RegistrySweeper{db: db, interval: interval}
}

func (s *RegistrySweeper) Run(ctx context.Context) {
	runEvery(ctx, s.interval, func(ctx context.Context) {
		result, err := s.tracedSweep(ctx)
		if err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "registry credential sweep failed", "error", err)
		}
		if result.CredentialsDeleted > 0 {
			slog.InfoContext(ctx, "registry credential sweep finished", "deleted", result.CredentialsDeleted)
		}
	})
}

func (s *RegistrySweeper) tracedSweep(ctx context.Context) (RegistrySweepResult, error) {
	ctx, span := tracing.StartRoot(ctx, registrySweepSpan)
	result, err := s.Sweep(ctx)
	span.SetAttributes(
		attribute.Bool(sweepRanAttr, result.Ran),
		attribute.Int64(credentialsDeletedAttr, result.CredentialsDeleted),
	)
	tracing.End(span, err)
	return result, err
}

func (s *RegistrySweeper) Sweep(ctx context.Context) (RegistrySweepResult, error) {
	var result RegistrySweepResult
	ran, err := runLocked(ctx, s.db, advisorylock.RegistrySweep, func() error {
		return s.deleteCredentials(ctx, &result)
	})
	result.Ran = ran
	return result, err
}

func (s *RegistrySweeper) deleteCredentials(ctx context.Context, result *RegistrySweepResult) error {
	queries := genDb.New(s.db)
	expired, err := queries.DeleteExpiredPullRegistryCredentials(ctx)
	result.CredentialsDeleted = expired
	if err != nil {
		return fmt.Errorf("delete expired pull registry credentials: %w", err)
	}
	stopped, err := queries.DeleteStoppedBuildRegistryCredentials(ctx)
	result.CredentialsDeleted += stopped
	if err != nil {
		return fmt.Errorf("delete stopped build registry credentials: %w", err)
	}
	return nil
}
