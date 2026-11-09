package sweepers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/advisorylock"
	"github.com/team-loco/loco/api/pkg/buildsource"
	"github.com/team-loco/loco/api/pkg/sourcebucket"
	"github.com/team-loco/loco/api/pkg/tracing"
	"go.opentelemetry.io/otel/attribute"
)

const (
	sourceSweepSpan     = "source.sweep"
	sourceExpiredAttr   = "loco.sweep.expired"
	sourcesDeletedAttr  = "loco.sweep.sources_deleted"
	orphansDeletedAttr  = "loco.sweep.orphans_deleted"
	sourceUploadExpired = "source upload expired"
)

// SourceBucket is the part of the source bucket the sweeper uses.
type SourceBucket interface {
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix, startAfter string, limit int32) ([]sourcebucket.Object, bool, error)
}

type SourceSweepConfig struct {
	Interval       time.Duration
	UploadGrace    time.Duration
	OrphanMinAge   time.Duration
	BuildBatch     int32
	OrphanPageSize int32
	OrphanMaxPages int
}

type SourceSweepResult struct {
	Ran            bool
	Expired        int
	SourcesDeleted int
	OrphansDeleted int
}

type SourceSweeper struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	bucket  SourceBucket
	config  SourceSweepConfig
	now     func() time.Time
}

func NewSourceSweeper(
	db *pgxpool.Pool,
	queries genDb.Querier,
	bucket SourceBucket,
	config SourceSweepConfig,
) *SourceSweeper {
	return &SourceSweeper{
		db:      db,
		queries: queries,
		bucket:  bucket,
		config:  config,
		now:     time.Now,
	}
}

func (s *SourceSweeper) Run(ctx context.Context) {
	runEvery(ctx, s.config.Interval, func(ctx context.Context) {
		result, err := s.tracedSweep(ctx)
		if err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "source sweep failed", "error", err)
		}
		if result.Expired+result.SourcesDeleted+result.OrphansDeleted > 0 {
			slog.InfoContext(ctx, "source sweep finished",
				"expired", result.Expired,
				"sourcesDeleted", result.SourcesDeleted,
				"orphansDeleted", result.OrphansDeleted,
			)
		}
	})
}

func (s *SourceSweeper) tracedSweep(ctx context.Context) (SourceSweepResult, error) {
	ctx, span := tracing.StartRoot(ctx, sourceSweepSpan)
	result, err := s.Sweep(ctx)
	span.SetAttributes(
		attribute.Bool(sweepRanAttr, result.Ran),
		attribute.Int(sourceExpiredAttr, result.Expired),
		attribute.Int(sourcesDeletedAttr, result.SourcesDeleted),
		attribute.Int(orphansDeletedAttr, result.OrphansDeleted),
	)
	tracing.End(span, err)
	return result, err
}

func (s *SourceSweeper) Sweep(ctx context.Context) (SourceSweepResult, error) {
	var result SourceSweepResult
	ran, err := runLocked(ctx, s.db, advisorylock.SourceSweep, func() error {
		return s.sweepAll(ctx, &result)
	})
	result.Ran = ran
	return result, err
}

func (s *SourceSweeper) sweepAll(ctx context.Context, result *SourceSweepResult) error {
	var err error
	result.Expired, err = s.expireUploads(ctx)
	if err != nil {
		return err
	}
	result.SourcesDeleted, err = s.deleteFinishedSources(ctx)
	if err != nil {
		return err
	}
	result.OrphansDeleted, err = s.deleteOrphans(ctx)
	return err
}

func (s *SourceSweeper) expireUploads(ctx context.Context) (int, error) {
	now := s.now()
	cutoff := now.Add(-(buildsource.UploadURLTTL + s.config.UploadGrace))
	var expired int
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		qtx := genDb.New(tx)
		rows, err := qtx.ExpireAwaitingUploadBuilds(ctx, genDb.ExpireAwaitingUploadBuildsParams{
			Message:       sourceUploadExpired,
			CreatedBefore: cutoff,
			MaxBuilds:     s.config.BuildBatch,
		})
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := events.RecordBuildFailure(ctx, qtx, row.ID, row.ResourceID, sourceUploadExpired); err != nil {
				return err
			}
		}
		expired = len(rows)
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("expire awaiting uploads: %w", err)
	}
	return expired, nil
}

func (s *SourceSweeper) deleteFinishedSources(ctx context.Context) (int, error) {
	rows, err := s.queries.ListUndeletedBuildSources(ctx, s.config.BuildBatch)
	if err != nil {
		return 0, fmt.Errorf("list undeleted sources: %w", err)
	}
	deleted := 0
	for _, row := range rows {
		if deleteErr := buildsource.Delete(ctx, s.queries, s.bucket, row.ID, row.SourceKey); deleteErr != nil {
			slog.WarnContext(ctx, "failed to delete build source", "buildId", row.ID, "error", deleteErr)
			continue
		}
		deleted++
	}
	return deleted, nil
}

func (s *SourceSweeper) deleteOrphans(ctx context.Context) (int, error) {
	now := s.now()
	cutoff := now.Add(-s.config.OrphanMinAge)
	deleted := 0
	after := ""
	for range s.config.OrphanMaxPages {
		objects, more, err := s.bucket.List(ctx, buildsource.KeyPrefix, after, s.config.OrphanPageSize)
		if err != nil {
			return deleted, fmt.Errorf("list sources: %w", err)
		}
		var candidates []string
		for _, object := range objects {
			if object.LastModified.Before(cutoff) {
				candidates = append(candidates, object.Key)
			}
		}
		n, err := s.deleteInactiveKeys(ctx, candidates)
		deleted += n
		if err != nil {
			return deleted, err
		}
		if !more || len(objects) == 0 {
			return deleted, nil
		}
		after = objects[len(objects)-1].Key
	}
	return deleted, nil
}

func (s *SourceSweeper) deleteInactiveKeys(ctx context.Context, keys []string) (int, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	active, err := s.queries.ListActiveBuildSourceKeys(ctx, keys)
	if err != nil {
		return 0, fmt.Errorf("list active source keys: %w", err)
	}
	keep := make(map[string]struct{}, len(active))
	for _, key := range active {
		keep[key] = struct{}{}
	}
	deleted := 0
	for _, key := range keys {
		if _, ok := keep[key]; ok {
			continue
		}
		if deleteErr := s.bucket.Delete(ctx, key); deleteErr != nil {
			slog.WarnContext(ctx, "failed to delete orphaned source", "key", key, "error", deleteErr)
			continue
		}
		deleted++
	}
	return deleted, nil
}
