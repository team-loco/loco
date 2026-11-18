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
	// placementWindowKeep must exceed any ingest lateness the observability proxy allows.
	placementWindowKeep      = 24 * time.Hour
	placementWindowSweepSpan = "placement_window.sweep"
	windowsDeletedAttr       = "loco.sweep.placement_windows_deleted"
)

type PlacementWindowSweepResult struct {
	Ran            bool
	WindowsDeleted int64
}

// PlacementWindowSweeper deletes placement windows closed longer ago than placementWindowKeep.
type PlacementWindowSweeper struct {
	db       *pgxpool.Pool
	interval time.Duration
}

func NewPlacementWindowSweeper(db *pgxpool.Pool, interval time.Duration) *PlacementWindowSweeper {
	return &PlacementWindowSweeper{db: db, interval: interval}
}

func (s *PlacementWindowSweeper) Run(ctx context.Context) {
	runEvery(ctx, s.interval, func(ctx context.Context) {
		result, err := s.tracedSweep(ctx)
		if err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "placement window sweep failed", "error", err)
		}
		if result.WindowsDeleted > 0 {
			slog.InfoContext(ctx, "placement window sweep finished", "deleted", result.WindowsDeleted)
		}
	})
}

func (s *PlacementWindowSweeper) tracedSweep(ctx context.Context) (PlacementWindowSweepResult, error) {
	ctx, span := tracing.StartRoot(ctx, placementWindowSweepSpan)
	result, err := s.Sweep(ctx)
	span.SetAttributes(
		attribute.Bool(sweepRanAttr, result.Ran),
		attribute.Int64(windowsDeletedAttr, result.WindowsDeleted),
	)
	tracing.End(span, err)
	return result, err
}

func (s *PlacementWindowSweeper) Sweep(ctx context.Context) (PlacementWindowSweepResult, error) {
	var result PlacementWindowSweepResult
	ran, err := runLocked(ctx, s.db, advisorylock.PlacementWindowSweep, func() error {
		deleted, err := genDb.New(s.db).DeleteExpiredPlacementWindows(ctx, time.Now().Add(-placementWindowKeep))
		result.WindowsDeleted = deleted
		if err != nil {
			return fmt.Errorf("delete expired placement windows: %w", err)
		}
		return nil
	})
	result.Ran = ran
	return result, err
}
