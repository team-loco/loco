package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
)

const (
	sourceSweepInterval         = 10 * time.Minute
	sourceUploadGrace           = time.Hour
	sourceOrphanMinAge          = 24 * time.Hour
	sourceSweepBatch            = 100
	sourceOrphanPageSize        = 1000
	sourceOrphanMaxPages        = 5
	sourceSweepLockKey    int64 = 0x6c6f636f0001
	sourceUploadExpired         = "source upload expired"
	advisoryUnlockTimeout       = 5 * time.Second
)

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
	now     func() time.Time
}

func NewSourceSweeper(db *pgxpool.Pool, queries genDb.Querier, bucket SourceBucket) *SourceSweeper {
	return &SourceSweeper{
		db:      db,
		queries: queries,
		bucket:  bucket,
		now:     time.Now,
	}
}

func (s *SourceSweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(sourceSweepInterval)
	defer ticker.Stop()
	for {
		result, err := s.Sweep(ctx)
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
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *SourceSweeper) Sweep(ctx context.Context) (SourceSweepResult, error) {
	var result SourceSweepResult
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return result, fmt.Errorf("acquire connection: %w", err)
	}
	lockQueries := genDb.New(conn)
	locked, err := lockQueries.TryAdvisoryLock(ctx, sourceSweepLockKey)
	if err != nil {
		conn.Release()
		return result, fmt.Errorf("lock source sweep: %w", err)
	}
	if !locked {
		conn.Release()
		return result, nil
	}
	defer unlockAdvisory(ctx, conn, lockQueries, sourceSweepLockKey)
	result.Ran = true

	expired, err := s.expireUploads(ctx)
	result.Expired = expired
	if err != nil {
		return result, err
	}

	deleted, err := s.deleteFinishedSources(ctx)
	result.SourcesDeleted = deleted
	if err != nil {
		return result, err
	}

	orphans, err := s.deleteOrphans(ctx)
	result.OrphansDeleted = orphans
	return result, err
}

func unlockAdvisory(ctx context.Context, conn *pgxpool.Conn, lockQueries *genDb.Queries, lockKey int64) {
	detached := context.WithoutCancel(ctx)
	unlockCtx, cancel := context.WithTimeout(detached, advisoryUnlockTimeout)
	defer cancel()
	unlocked, err := lockQueries.AdvisoryUnlock(unlockCtx, lockKey)
	if err == nil && unlocked {
		conn.Release()
		return
	}
	slog.WarnContext(ctx, "failed to release an advisory lock, closing its connection",
		"lockKey", lockKey,
		"error", err,
	)
	raw := conn.Hijack()
	if closeErr := raw.Close(unlockCtx); closeErr != nil {
		slog.WarnContext(ctx, "failed to close the advisory lock connection", "lockKey", lockKey, "error", closeErr)
	}
}

func (s *SourceSweeper) expireUploads(ctx context.Context) (int, error) {
	now := s.now()
	cutoff := now.Add(-(buildUploadURLTTL + sourceUploadGrace))
	ids, err := s.queries.ExpireAwaitingUploadBuilds(ctx, genDb.ExpireAwaitingUploadBuildsParams{
		Message:       sourceUploadExpired,
		CreatedBefore: cutoff,
		MaxBuilds:     sourceSweepBatch,
	})
	if err != nil {
		return 0, fmt.Errorf("expire awaiting uploads: %w", err)
	}
	return len(ids), nil
}

func (s *SourceSweeper) deleteFinishedSources(ctx context.Context) (int, error) {
	rows, err := s.queries.ListUndeletedBuildSources(ctx, sourceSweepBatch)
	if err != nil {
		return 0, fmt.Errorf("list undeleted sources: %w", err)
	}
	deleted := 0
	for _, row := range rows {
		if deleteErr := deleteBuildSource(ctx, s.queries, s.bucket, row.ID, row.SourceKey); deleteErr != nil {
			slog.WarnContext(ctx, "failed to delete build source", "buildId", row.ID, "error", deleteErr)
			continue
		}
		deleted++
	}
	return deleted, nil
}

func (s *SourceSweeper) deleteOrphans(ctx context.Context) (int, error) {
	now := s.now()
	cutoff := now.Add(-sourceOrphanMinAge)
	deleted := 0
	after := ""
	for range sourceOrphanMaxPages {
		objects, more, err := s.bucket.List(ctx, buildSourcePrefix, after, sourceOrphanPageSize)
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

func deleteBuildSource(
	ctx context.Context,
	queries genDb.Querier,
	bucket SourceBucket,
	buildID uuid.UUID,
	key string,
) error {
	if err := bucket.Delete(ctx, key); err != nil {
		return fmt.Errorf("delete source: %w", err)
	}
	if err := queries.MarkBuildSourceDeleted(ctx, buildID); err != nil {
		return fmt.Errorf("mark source deleted: %w", err)
	}
	return nil
}
