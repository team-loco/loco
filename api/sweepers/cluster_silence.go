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
	"github.com/team-loco/loco/api/pkg/tracing"
)

const (
	clusterSilenceSweepSpan = "cluster_silence.sweep"
	clustersSilencedAttr    = "loco.sweep.clusters_silenced"
)

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
		if err := s.tracedSweep(ctx); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "cluster silence sweep failed", "error", err)
		}
	})
}

func (s *ClusterSilenceSweeper) tracedSweep(ctx context.Context) error {
	ctx, span := tracing.StartRoot(ctx, clusterSilenceSweepSpan)
	silenced, err := s.Sweep(ctx)
	span.SetAttributes(attribute.Int(clustersSilencedAttr, silenced))
	tracing.End(span, err)
	return err
}

// Sweep records cluster.silent for each cluster it newly finds silent and returns how many. Replicas may sweep
// at once: the conditional update of silent_since lets exactly one of them record each silence.
func (s *ClusterSilenceSweeper) Sweep(ctx context.Context) (int, error) {
	var silenced int
	err := pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := genDb.New(tx)
		marked, err := q.MarkSilentClusters(ctx, time.Now().Add(-s.silentAfter))
		if err != nil {
			return fmt.Errorf("mark silent clusters: %w", err)
		}
		for _, c := range marked {
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
		silenced = len(marked)
		return nil
	})
	return silenced, err
}
