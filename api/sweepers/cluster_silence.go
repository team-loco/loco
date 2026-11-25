package sweepers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
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
	clustersRecoveredAttr   = "loco.sweep.clusters_recovered"
)

type ClusterSilenceSweepResult struct {
	Ran       bool
	Silenced  int
	Recovered int
}

// ClusterSilenceSweeper records cluster.silent for a cluster whose heartbeat is older than silentAfter and
// cluster.recovered when it reports again, once per transition.
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
		attribute.Int(clustersRecoveredAttr, result.Recovered),
	)
	tracing.End(span, err)
	return result, err
}

func (s *ClusterSilenceSweeper) Sweep(ctx context.Context) (ClusterSilenceSweepResult, error) {
	var result ClusterSilenceSweepResult
	ran, err := runLocked(ctx, s.db, advisorylock.ClusterSilenceSweep, func() error {
		return pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
			q := genDb.New(tx)
			now := time.Now()
			heardSince := now.Add(-s.silentAfter)
			recovered, err := q.ClearRecoveredClusters(ctx, heardSince)
			if err != nil {
				return fmt.Errorf("clear recovered clusters: %w", err)
			}
			for _, c := range recovered {
				silent := now.Sub(c.SilentSince).Round(time.Second)
				data := map[string]any{events.FieldSilentSeconds: int64(silent / time.Second)}
				if err = recordClusterEvent(ctx, q, events.ClusterRecovered, c.ID, c.Name, c.Region, data); err != nil {
					return err
				}
			}
			silenced, err := q.MarkSilentClusters(ctx, heardSince)
			if err != nil {
				return fmt.Errorf("mark silent clusters: %w", err)
			}
			for _, c := range silenced {
				if err = recordClusterEvent(ctx, q, events.ClusterSilent, c.ID, c.Name, c.Region, nil); err != nil {
					return err
				}
			}
			result.Silenced, result.Recovered = len(silenced), len(recovered)
			return nil
		})
	})
	result.Ran = ran
	return result, err
}

func recordClusterEvent(
	ctx context.Context,
	q genDb.Querier,
	eventType string,
	id uuid.UUID,
	name, region string,
	data map[string]any,
) error {
	if data == nil {
		data = map[string]any{}
	}
	data[events.FieldName] = name
	data[events.FieldRegion] = region
	if err := events.Record(ctx, q, events.Event{
		Type:        eventType,
		ActorType:   events.ActorSystem,
		SubjectType: events.SubjectCluster,
		SubjectID:   &id,
		Data:        data,
	}); err != nil {
		return fmt.Errorf("record %s for cluster %s: %w", eventType, id, err)
	}
	return nil
}
