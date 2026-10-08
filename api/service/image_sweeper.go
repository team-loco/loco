package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
)

const (
	imageSweepLockKey   int64 = 0x6c6f636f0002
	workspacePathPrefix       = "ws-"
	repositoryPathParts       = 2
)

var (
	errForeignRepository = errors.New("image repository is not on the configured registry host")
	errBuildImageDeleted = errors.New(
		"the build's image was deleted from the registry by image retention; build the source again",
	)
)

type ImageRegistry interface {
	DeleteManifest(ctx context.Context, path, digest string) error
	Repositories(ctx context.Context, after string, limit int) ([]string, error)
	ManifestDigests(ctx context.Context, path string) ([]string, error)
}

type ImageSweepConfig struct {
	RegistryHost    string
	RegistryPrefix  string
	Retention       int32
	Interval        time.Duration
	BuildBatch      int32
	RepositoryBatch int
}

type ImageSweepResult struct {
	Ran                bool
	ImagesDeleted      int
	OrphanRepositories int
	OrphanManifests    int
}

type ImageSweeper struct {
	db       *pgxpool.Pool
	queries  genDb.Querier
	registry ImageRegistry
	config   ImageSweepConfig
	cursor   string
}

func NewImageSweeper(
	db *pgxpool.Pool,
	queries genDb.Querier,
	registry ImageRegistry,
	config ImageSweepConfig,
) *ImageSweeper {
	return &ImageSweeper{
		db:       db,
		queries:  queries,
		registry: registry,
		config:   config,
	}
}

func (s *ImageSweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.config.Interval)
	defer ticker.Stop()
	for {
		result, err := s.Sweep(ctx)
		if err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "image sweep failed", "error", err)
		}
		if result.ImagesDeleted+result.OrphanManifests > 0 {
			slog.InfoContext(ctx, "image sweep finished",
				"imagesDeleted", result.ImagesDeleted,
				"orphanRepositories", result.OrphanRepositories,
				"orphanManifests", result.OrphanManifests,
			)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *ImageSweeper) Sweep(ctx context.Context) (ImageSweepResult, error) {
	var result ImageSweepResult
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return result, fmt.Errorf("acquire connection: %w", err)
	}
	lockQueries := genDb.New(conn)
	locked, err := lockQueries.TryAdvisoryLock(ctx, imageSweepLockKey)
	if err != nil {
		conn.Release()
		return result, fmt.Errorf("lock image sweep: %w", err)
	}
	if !locked {
		conn.Release()
		return result, nil
	}
	defer unlockAdvisory(ctx, conn, lockQueries, imageSweepLockKey)
	result.Ran = true

	deleted, err := s.deleteExpiredImages(ctx)
	result.ImagesDeleted = deleted
	if err != nil {
		return result, err
	}

	repositories, manifests, err := s.deleteOrphanRepositories(ctx)
	result.OrphanRepositories = repositories
	result.OrphanManifests = manifests
	return result, err
}

func (s *ImageSweeper) deleteExpiredImages(ctx context.Context) (int, error) {
	rows, err := s.queries.ListDeletableBuildImages(ctx, genDb.ListDeletableBuildImagesParams{
		Keep:      s.config.Retention,
		MaxBuilds: s.config.BuildBatch,
	})
	if err != nil {
		return 0, fmt.Errorf("list deletable build images: %w", err)
	}
	deleted := 0
	for _, row := range rows {
		removed, deleteErr := s.deleteBuildImage(ctx, row.ID)
		if deleteErr != nil {
			slog.WarnContext(ctx, "failed to delete build image", "buildId", row.ID, "error", deleteErr)
			continue
		}
		if removed {
			deleted++
		}
	}
	return deleted, nil
}

func (s *ImageSweeper) deleteBuildImage(ctx context.Context, buildID uuid.UUID) (bool, error) {
	removed := false
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		removed = false
		deletedAt, lockErr := qtx.LockBuildImageForDelete(ctx, buildID)
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return nil
		}
		if lockErr != nil {
			return fmt.Errorf("lock build: %w", lockErr)
		}
		if deletedAt != nil {
			return nil
		}
		rows, listErr := qtx.ListDeletableBuildImages(ctx, genDb.ListDeletableBuildImagesParams{
			Keep:      s.config.Retention,
			BuildID:   &buildID,
			MaxBuilds: 1,
		})
		if listErr != nil {
			return fmt.Errorf("recheck build image: %w", listErr)
		}
		if len(rows) == 0 {
			return nil
		}
		row := rows[0]
		path, pathErr := registryPath(s.config.RegistryHost, row.ImageRepository)
		if pathErr != nil {
			return pathErr
		}
		if deleteErr := s.registry.DeleteManifest(ctx, path, row.ImageDigest); deleteErr != nil {
			return fmt.Errorf("delete image: %w", deleteErr)
		}
		if row.CacheDigest != nil {
			if deleteErr := s.registry.DeleteManifest(ctx, path, *row.CacheDigest); deleteErr != nil {
				return fmt.Errorf("delete cache: %w", deleteErr)
			}
		}
		if _, markErr := qtx.MarkBuildImageDeleted(ctx, buildID); markErr != nil {
			return fmt.Errorf("mark image deleted: %w", markErr)
		}
		removed = true
		return nil
	})
	return removed, err
}

func (s *ImageSweeper) deleteOrphanRepositories(ctx context.Context) (int, int, error) {
	repos, err := s.registry.Repositories(ctx, s.cursor, s.config.RepositoryBatch)
	if err != nil {
		return 0, 0, fmt.Errorf("list repositories: %w", err)
	}
	s.cursor = ""
	if len(repos) == s.config.RepositoryBatch {
		s.cursor = repos[len(repos)-1]
	}

	byResource := map[uuid.UUID][]string{}
	var ids []uuid.UUID
	for _, repo := range repos {
		resourceID, ok := parseRepositoryPath(s.config.RegistryPrefix, repo)
		if !ok {
			continue
		}
		if _, seen := byResource[resourceID]; !seen {
			ids = append(ids, resourceID)
		}
		byResource[resourceID] = append(byResource[resourceID], repo)
	}
	if len(ids) == 0 {
		return 0, 0, nil
	}
	existing, err := s.queries.ListExistingResourceIDs(ctx, ids)
	if err != nil {
		return 0, 0, fmt.Errorf("list existing resources: %w", err)
	}
	for _, id := range existing {
		delete(byResource, id)
	}

	orphanRepos := 0
	orphanManifests := 0
	for _, paths := range byResource {
		for _, path := range paths {
			n, purgeErr := s.purgeRepository(ctx, path)
			orphanManifests += n
			if purgeErr != nil {
				slog.WarnContext(ctx, "failed to purge orphaned repository", "repository", path, "error", purgeErr)
				continue
			}
			if n > 0 {
				orphanRepos++
			}
		}
	}
	return orphanRepos, orphanManifests, nil
}

func (s *ImageSweeper) purgeRepository(ctx context.Context, path string) (int, error) {
	digests, err := s.registry.ManifestDigests(ctx, path)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, digest := range digests {
		if deleteErr := s.registry.DeleteManifest(ctx, path, digest); deleteErr != nil {
			return deleted, deleteErr
		}
		deleted++
	}
	return deleted, nil
}

func registryPath(registryHost, imageRepository string) (string, error) {
	host := strings.TrimSuffix(registryHost, "/")
	path, ok := strings.CutPrefix(imageRepository, host+"/")
	if !ok || path == "" {
		return "", fmt.Errorf("%w: %q", errForeignRepository, imageRepository)
	}
	return path, nil
}

func parseRepositoryPath(registryPrefix, path string) (uuid.UUID, bool) {
	rest := path
	prefix := strings.Trim(registryPrefix, "/")
	if prefix != "" {
		var ok bool
		rest, ok = strings.CutPrefix(path, prefix+"/")
		if !ok {
			return uuid.UUID{}, false
		}
	}
	parts := strings.Split(rest, "/")
	if len(parts) != repositoryPathParts {
		return uuid.UUID{}, false
	}
	rawWorkspaceID, ok := strings.CutPrefix(parts[0], workspacePathPrefix)
	if !ok {
		return uuid.UUID{}, false
	}
	if _, err := uuid.Parse(rawWorkspaceID); err != nil {
		return uuid.UUID{}, false
	}
	resourceID, err := uuid.Parse(parts[1])
	if err != nil {
		return uuid.UUID{}, false
	}
	return resourceID, true
}
