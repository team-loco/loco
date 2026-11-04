package service

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
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		result, err := s.tracedSweep(ctx)
		if err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "registry credential sweep failed", "error", err)
		}
		if result.CredentialsDeleted > 0 {
			slog.InfoContext(ctx, "registry credential sweep finished", "deleted", result.CredentialsDeleted)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
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
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return result, fmt.Errorf("acquire connection: %w", err)
	}
	lockQueries := genDb.New(conn)
	locked, err := lockQueries.TryAdvisoryLock(ctx, advisorylock.RegistrySweep)
	if err != nil {
		conn.Release()
		return result, fmt.Errorf("lock registry credential sweep: %w", err)
	}
	if !locked {
		conn.Release()
		return result, nil
	}
	defer unlockAdvisory(ctx, conn, lockQueries, advisorylock.RegistrySweep)
	result.Ran = true

	expired, err := lockQueries.DeleteExpiredPullRegistryCredentials(ctx)
	result.CredentialsDeleted = expired
	if err != nil {
		return result, fmt.Errorf("delete expired pull registry credentials: %w", err)
	}
	stopped, err := lockQueries.DeleteStoppedBuildRegistryCredentials(ctx)
	result.CredentialsDeleted += stopped
	if err != nil {
		return result, fmt.Errorf("delete stopped build registry credentials: %w", err)
	}
	return result, nil
}
