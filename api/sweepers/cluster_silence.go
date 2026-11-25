package sweepers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/advisorylock"
	"github.com/team-loco/loco/api/pkg/tracing"
)

const (
	clusterSilenceSweepSpan = "cluster_silence.sweep"
	clustersSilencedAttr    = "loco.sweep.clusters_silenced"
)

type ClusterSilenceSweepResult struct {
	Ran      bool
	Silenced int
}

// ClusterSilenceSweeper records cluster.silent once for a cluster whose last heartbeat is older than silentAfter.
// The heartbeat that ends the silence records cluster.recovered.
type ClusterSilenceSweeper struct {
	db          *pgxpool.Pool
	silentAfter time.Duration
	interval    time.Duration
}

func NewClusterSilenceSweeper(db *pgxpool.Pool, silentAfter, interval time.Duration) *ClusterSilenceSweeper {
	return &ClusterSilenceSweeper{db: db, silentAfter: silentAfter, interval: interval}
}

func (s *ClusterSilenceSweeper) Run(ctx context.Context) {
	runEvery(ctx, s.interval, func(ctx context.Context) {
		if _, err := s.tracedSweep(ctx); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "cluster silence sweep failed", "error", err)
		}
	})
}

func (s *ClusterSilenceSweeper) tracedSweep(ctx context.Context) (ClusterSilenceSweepResult, error) {
	ctx, span := tracing.StartRoot(ctx, clusterSilenceSweepSpan)
	result, err := s.Sweep(ctx)
	span.SetAttributes(
		attribute.Bool(sweepRanAttr, result.Ran),
		attribute.Int(clustersSilencedAttr, result.Silenced),
	)
	tracing.End(span, err)
	return result, err
}

func (s *ClusterSilenceSweeper) Sweep(ctx context.Context) (ClusterSilenceSweepResult, error) {
	var result ClusterSilenceSweepResult
	ran, err := runLocked(ctx, s.db, advisorylock.ClusterSilenceSweep, func() error {
		return pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
			q := genDb.New(tx)
			silenced, err := q.MarkSilentClusters(ctx, time.Now().Add(-s.silentAfter))
			if err != nil {
				return fmt.Errorf("mark silent clusters: %w", err)
			}
			for _, c := range silenced {
				err = events.Record(ctx, q, events.Event{
					Type:        events.ClusterSilent,
					ActorType:   events.ActorSystem,
					SubjectType: events.SubjectCluster,
					SubjectID:   new(c.ID),
					Data:        map[string]any{events.FieldName: c.Name, events.FieldRegion: c.Region},
				})
				if err != nil {
					return fmt.Errorf("record cluster.silent for cluster %s: %w", c.ID, err)
				}
			}
			result.Silenced = len(silenced)
			return nil
		})
	})
	result.Ran = ran
	return result, err
}
