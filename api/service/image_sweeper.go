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
	"github.com/team-loco/loco/api/pkg/registryclient"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	imageSweepLockKey      int64 = 0x6c6f636f0002
	workspacePathPrefix          = "ws-"
	repositoryPathParts          = 2
	uuidVersionTimeOrdered       = 7
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
	Tags(ctx context.Context, path string) ([]string, error)
	TagDigest(ctx context.Context, path, tag string) (string, error)
}

type ImageSweepConfig struct {
	RegistryHost    string
	RegistryPrefix  string
	Retention       int32
	Interval        time.Duration
	BuildBatch      int32
	RepositoryBatch int
	TagBatch        int
	TagMinAge       time.Duration
}

type ImageSweepResult struct {
	Ran                bool
	ImagesDeleted      int
	OrphanRepositories int
	OrphanManifests    int
	StaleTags          int
}

type ImageSweeper struct {
	db       *pgxpool.Pool
	queries  genDb.Querier
	registry ImageRegistry
	config   ImageSweepConfig
	cursor   string
	now      func() time.Time
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
		now:      time.Now,
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
		if result.ImagesDeleted+result.OrphanManifests+result.StaleTags > 0 {
			slog.InfoContext(ctx, "image sweep finished",
				"imagesDeleted", result.ImagesDeleted,
				"orphanRepositories", result.OrphanRepositories,
				"orphanManifests", result.OrphanManifests,
				"staleTags", result.StaleTags,
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

	err = s.sweepRepositories(ctx, &result)
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

func (s *ImageSweeper) sweepRepositories(ctx context.Context, result *ImageSweepResult) error {
	repos, err := s.registry.Repositories(ctx, s.cursor, s.config.RepositoryBatch)
	if err != nil {
		return fmt.Errorf("list repositories: %w", err)
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
		return nil
	}
	existing, err := s.queries.ListExistingResourceIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("list existing resources: %w", err)
	}
	live := map[uuid.UUID][]string{}
	for _, id := range existing {
		live[id] = byResource[id]
		delete(byResource, id)
	}

	for _, paths := range byResource {
		for _, path := range paths {
			n, purgeErr := s.purgeRepository(ctx, path)
			result.OrphanManifests += n
			if purgeErr != nil {
				slog.WarnContext(ctx, "failed to purge orphaned repository", "repository", path, "error", purgeErr)
				continue
			}
			if n > 0 {
				result.OrphanRepositories++
			}
		}
	}

	stale, err := s.deleteStaleTags(ctx, live)
	result.StaleTags = stale
	return err
}

func (s *ImageSweeper) purgeRepository(ctx context.Context, path string) (int, error) {
	tags, err := s.registry.Tags(ctx, path)
	if err != nil {
		return 0, err
	}
	deleted := map[string]struct{}{}
	for _, tag := range tags {
		digest, digestErr := s.registry.TagDigest(ctx, path, tag)
		if errors.Is(digestErr, registryclient.ErrTagNotFound) {
			continue
		}
		if digestErr != nil {
			return len(deleted), digestErr
		}
		if _, done := deleted[digest]; done {
			continue
		}
		if deleteErr := s.registry.DeleteManifest(ctx, path, digest); deleteErr != nil {
			return len(deleted), deleteErr
		}
		deleted[digest] = struct{}{}
	}
	return len(deleted), nil
}

type buildTag struct {
	path       string
	tag        string
	buildID    uuid.UUID
	resourceID uuid.UUID
}

func parseBuildTag(tag string) (uuid.UUID, bool) {
	rawID, ok := strings.CutPrefix(tag, locoControllerV1.BuildCacheTagPrefix)
	if !ok {
		rawID, ok = strings.CutPrefix(tag, locoControllerV1.BuildImageTagPrefix)
	}
	if !ok {
		return uuid.UUID{}, false
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		return uuid.UUID{}, false
	}
	return id, true
}

func (s *ImageSweeper) listBuildTags(ctx context.Context, live map[uuid.UUID][]string) ([]buildTag, error) {
	var tags []buildTag
	for resourceID, paths := range live {
		for _, path := range paths {
			names, err := s.registry.Tags(ctx, path)
			if err != nil {
				return nil, fmt.Errorf("list tags of %s: %w", path, err)
			}
			for _, name := range names {
				buildID, ok := parseBuildTag(name)
				if !ok {
					continue
				}
				tags = append(tags, buildTag{path: path, tag: name, buildID: buildID, resourceID: resourceID})
			}
		}
	}
	return tags, nil
}

func (s *ImageSweeper) deleteStaleTags(ctx context.Context, live map[uuid.UUID][]string) (int, error) {
	if len(live) == 0 {
		return 0, nil
	}
	tags, err := s.listBuildTags(ctx, live)
	if err != nil {
		return 0, err
	}
	if len(tags) == 0 {
		return 0, nil
	}
	buildIDs := make([]uuid.UUID, 0, len(tags))
	for _, tag := range tags {
		buildIDs = append(buildIDs, tag.buildID)
	}
	states, err := s.queries.ListBuildTagStates(ctx, buildIDs)
	if err != nil {
		return 0, fmt.Errorf("list build states: %w", err)
	}
	byID := make(map[uuid.UUID]genDb.ListBuildTagStatesRow, len(states))
	for _, state := range states {
		byID[state.ID] = state
	}
	resourceIDs := make([]uuid.UUID, 0, len(live))
	for id := range live {
		resourceIDs = append(resourceIDs, id)
	}
	protected, err := s.protectedDigests(ctx, resourceIDs)
	if err != nil {
		return 0, err
	}

	now := s.now()
	cutoff := now.Add(-s.config.TagMinAge)
	budget := s.config.TagBatch
	deleted := 0
	for _, tag := range tags {
		if budget == 0 {
			break
		}
		state, found := byID[tag.buildID]
		if !tagExpired(tag, state, found, cutoff) {
			continue
		}
		budget--
		removed, deleteErr := s.deleteTag(ctx, tag, protected[tag.resourceID])
		if deleteErr != nil {
			slog.WarnContext(ctx, "failed to delete stale build tag",
				"repository", tag.path,
				"tag", tag.tag,
				"error", deleteErr,
			)
			continue
		}
		if removed {
			deleted++
		}
	}
	return deleted, nil
}

func (s *ImageSweeper) protectedDigests(
	ctx context.Context,
	resourceIDs []uuid.UUID,
) (map[uuid.UUID]map[string]struct{}, error) {
	rows, err := s.queries.ListLiveBuildDigests(ctx, resourceIDs)
	if err != nil {
		return nil, fmt.Errorf("list live build digests: %w", err)
	}
	protected := map[uuid.UUID]map[string]struct{}{}
	for _, row := range rows {
		digests, ok := protected[row.ResourceID]
		if !ok {
			digests = map[string]struct{}{}
			protected[row.ResourceID] = digests
		}
		digests[row.ImageDigest] = struct{}{}
		if row.CacheDigest != nil {
			digests[*row.CacheDigest] = struct{}{}
		}
	}
	return protected, nil
}

func tagExpired(tag buildTag, state genDb.ListBuildTagStatesRow, found bool, cutoff time.Time) bool {
	if !found {
		if tag.buildID.Version() != uuidVersionTimeOrdered {
			return false
		}
		sec, nsec := tag.buildID.Time().UnixTime()
		created := time.Unix(sec, nsec)
		return created.Before(cutoff)
	}
	if state.ResourceID != tag.resourceID || state.FinishedAt == nil {
		return false
	}
	switch state.Status {
	case genDb.BuildStatusFailed:
		return state.FinishedAt.Before(cutoff)
	case genDb.BuildStatusCanceled:
		return state.FinishedAt.Before(cutoff)
	default:
		return false
	}
}

func (s *ImageSweeper) deleteTag(ctx context.Context, tag buildTag, protected map[string]struct{}) (bool, error) {
	digest, err := s.registry.TagDigest(ctx, tag.path, tag.tag)
	if errors.Is(err, registryclient.ErrTagNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, ok := protected[digest]; ok {
		return false, nil
	}
	if deleteErr := s.registry.DeleteManifest(ctx, tag.path, digest); deleteErr != nil {
		return false, deleteErr
	}
	return true, nil
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
