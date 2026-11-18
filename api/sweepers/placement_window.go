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
	placementWindowSweepSpan = "placement_window.sweep"
	windowsDeletedAttr       = "loco.sweep.placement_windows_deleted"
)

type PlacementWindowSweepResult struct {
	Ran            bool
	WindowsDeleted int64
}

// PlacementWindowSweeper deletes placement windows closed longer ago than keep.
type PlacementWindowSweeper struct {
	db       *pgxpool.Pool
	interval time.Duration
	keep     time.Duration
}

func NewPlacementWindowSweeper(db *pgxpool.Pool, interval, keep time.Duration) *PlacementWindowSweeper {
	return &PlacementWindowSweeper{db: db, interval: interval, keep: keep}
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
		deleted, err := genDb.New(s.db).DeleteExpiredPlacementWindows(ctx, time.Now().Add(-s.keep))
		result.WindowsDeleted = deleted
		if err != nil {
			return fmt.Errorf("delete expired placement windows: %w", err)
		}
		return nil
	})
	result.Ran = ran
	return result, err
}
