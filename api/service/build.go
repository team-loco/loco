package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/buildsource"
	"github.com/team-loco/loco/api/pkg/imagepath"
	"github.com/team-loco/loco/api/pkg/sourcebucket"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	"github.com/team-loco/loco/internal/clip"
	"github.com/team-loco/loco/internal/rpcerr"
)

const (
	buildSourceTypeUpload = "upload"
	defaultBuildContext   = "."
	// maxGitCommitMessageRunes bounds the stored subject line; longer ones are clipped
	// rather than refused, so commit metadata never fails a build.
	maxGitCommitMessageRunes = 4096
)

var (
	ErrBuildNotFound    = rpcerr.NewCoded(connect.CodeNotFound, "build not found")
	ErrBuildsNotEnabled = errors.New(
		"builds are not enabled on this server: the source bucket and registry are not configured",
	)
	errBuildNotQueueable  = rpcerr.NewCoded(connect.CodeFailedPrecondition, "build is no longer awaiting upload")
	errBuildNotCancelable = rpcerr.NewCoded(connect.CodeFailedPrecondition, "build has already finished")
	errSourceNotUploaded  = errors.New("the source has not been uploaded")
	errNoBuildCluster     = errors.New("no cluster in this install accepts builds")
)

type SourceBucket interface {
	PresignPut(ctx context.Context, key string, size int64, ttl time.Duration) (string, error)
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	ObjectSize(ctx context.Context, key string) (int64, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix, startAfter string, limit int32) ([]sourcebucket.Object, bool, error)
}

type BuildConfig struct {
	RegistryHost   string
	RegistryPrefix string
	SourceMaxBytes int64
}

type BuildServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	authz   *authz.Authorizer
	bucket  SourceBucket
	config  BuildConfig
	now     func() time.Time
}

func NewBuildServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	bucket SourceBucket,
	config BuildConfig,
) *BuildServer {
	return &BuildServer{
		db:      db,
		queries: queries,
		authz:   authz.New(db, queries),
		bucket:  bucket,
		config:  config,
		now:     time.Now,
	}
}

func (s *BuildServer) enabled() bool {
	return s.bucket != nil && s.config.RegistryHost != ""
}

func buildsUnavailable(reason error) *connect.Error {
	connectErr := rpcerr.New(connect.CodeFailedPrecondition, reason)
	return withDetail(connectErr, &buildv1.BuildsUnavailable{})
}

func (s *BuildServer) CreateBuild(
	ctx context.Context,
	req *buildv1.CreateBuildRequest,
) (*buildv1.CreateBuildResponse, error) {
	if !s.enabled() {
		return nil, buildsUnavailable(ErrBuildsNotEnabled)
	}

	resourceID, err := parseID("resource_id", req.GetResourceId())
	if err != nil {
		return nil, err
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err = check(ctx, s.authz, p, actions.New(actions.CreateBuild, resourceID)); err != nil {
		return nil, err
	}

	if _, clusterErr := s.buildCluster(ctx); clusterErr != nil {
		return nil, clusterErr
	}

	sourceSize := req.GetSourceSize()
	if sourceSize > s.config.SourceMaxBytes {
		tooLarge := fmt.Errorf("source is %d bytes, over the %d byte limit", sourceSize, s.config.SourceMaxBytes)
		return nil, connect.NewError(connect.CodeInvalidArgument, publicSourceTooLarge).WithCause(tooLarge)
	}

	resource, err := s.queries.GetResourceByID(ctx, resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrResourceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get resource %s: %w", resourceID, err)
	}

	buildID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate build id: %w", err)
	}
	sourceKey := buildsource.Key(buildID)

	issuedAt := s.now()
	uploadURL, err := s.bucket.PresignPut(ctx, sourceKey, sourceSize, buildsource.UploadURLTTL)
	if err != nil {
		return nil, fmt.Errorf("presign source upload for build %s: %w", buildID, err)
	}

	repository := imagepath.Repository(
		s.config.RegistryHost,
		s.config.RegistryPrefix,
		resource.WorkspaceID,
		resource.ID,
	)
	dockerfilePath := req.GetDockerfilePath()
	var gitCommitMessage *string
	if req.GitCommitMessage != nil {
		gitCommitMessage = new(clip.Runes(req.GetGitCommitMessage(), maxGitCommitMessageRunes))
	}
	buildContext := req.GetContext()
	if buildContext == "" {
		buildContext = defaultBuildContext
	}
	createErr := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, err := qtx.CreateBuild(ctx, genDb.CreateBuildParams{
			ID:               buildID,
			ResourceID:       resource.ID,
			SourceType:       buildSourceTypeUpload,
			SourceKey:        sourceKey,
			SourceSize:       sourceSize,
			DockerfilePath:   dockerfilePath,
			Context:          buildContext,
			ImageRepository:  repository,
			CreatedBy:        p.Entity.ID,
			GitCommit:        req.GitCommit,
			GitBranch:        req.GitBranch,
			GitCommitMessage: gitCommitMessage,
		}); err != nil {
			return fmt.Errorf("create build: %w", err)
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.BuildCreated,
			WorkspaceID: new(resource.WorkspaceID),
			ResourceID:  new(resource.ID),
			SubjectType: events.SubjectBuild,
			SubjectID:   new(buildID),
			Data: map[string]any{
				events.FieldResourceID: resource.ID.String(),
				events.FieldSourceSize: sourceSize,
			},
		})
	})
	if createErr != nil {
		return nil, createErr
	}

	expiresAt := issuedAt.Add(buildsource.UploadURLTTL)
	uploadExpiresAt := timestamppb.New(expiresAt)
	buildIDStr := buildID.String()
	return &buildv1.CreateBuildResponse{
		BuildId:         buildIDStr,
		UploadUrl:       uploadURL,
		UploadExpiresAt: uploadExpiresAt,
	}, nil
}

func (s *BuildServer) StartBuild(
	ctx context.Context,
	req *buildv1.StartBuildRequest,
) (*buildv1.StartBuildResponse, error) {
	if !s.enabled() {
		return nil, buildsUnavailable(ErrBuildsNotEnabled)
	}

	rawBuildID := req.GetBuildId()
	build, err := s.getBuild(ctx, rawBuildID)
	if err != nil {
		return nil, err
	}

	if authErr := authorize(ctx, s.authz, actions.CreateBuild, build.ResourceID); authErr != nil {
		return nil, authErr
	}

	if build.Status != genDb.BuildStatusAwaitingUpload {
		notAwaiting := fmt.Errorf("build is %s, not awaiting upload", build.Status)
		return nil, rpcerr.New(connect.CodeFailedPrecondition, notAwaiting)
	}

	size, err := s.bucket.ObjectSize(ctx, build.SourceKey)
	if errors.Is(err, sourcebucket.ErrObjectNotFound) {
		return nil, rpcerr.New(connect.CodeFailedPrecondition, errSourceNotUploaded)
	}
	if err != nil {
		return nil, fmt.Errorf("check the uploaded source of build %s: %w", build.ID, err)
	}
	if size != build.SourceSize {
		mismatch := fmt.Errorf("the uploaded source is %d bytes, expected %d", size, build.SourceSize)
		return nil, rpcerr.New(connect.CodeFailedPrecondition, mismatch)
	}

	clusterID, err := s.buildCluster(ctx)
	if err != nil {
		return nil, err
	}

	superseded, err := queueBuild(ctx, s.db, build.ID, build.ResourceID, clusterID)
	if err != nil {
		return nil, fmt.Errorf("queue build %s: %w", build.ID, err)
	}
	deleteBuildSources(ctx, s.queries, s.bucket, superseded...)

	queued, err := s.getBuild(ctx, rawBuildID)
	if err != nil {
		return nil, err
	}
	queuedProto := buildToProto(queued)
	return &buildv1.StartBuildResponse{Build: queuedProto}, nil
}

func (s *BuildServer) buildCluster(ctx context.Context) (uuid.UUID, error) {
	clusterID, err := s.queries.GetBuildCluster(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, buildsUnavailable(errNoBuildCluster)
	}
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("get build cluster: %w", err)
	}
	return clusterID, nil
}

func queueBuild(
	ctx context.Context,
	pool *pgxpool.Pool,
	buildID, resourceID, clusterID uuid.UUID,
) ([]buildSource, error) {
	supersededMessage := "superseded by build " + buildID.String()
	var supersededSources []buildSource
	err := withTx(ctx, pool, func(qtx *genDb.Queries) error {
		supersededSources = nil
		if activeErr := ensureOrganizationActive(ctx, qtx, resourceID); activeErr != nil {
			return activeErr
		}
		if lockErr := lockResource(ctx, qtx, resourceID); lockErr != nil {
			return lockErr
		}

		if assignErr := lockAssignableCluster(ctx, qtx, clusterID); assignErr != nil {
			return assignErr
		}
		superseded, cancelErr := qtx.CancelOtherActiveBuilds(ctx, genDb.CancelOtherActiveBuildsParams{
			Message:    supersededMessage,
			ResourceID: resourceID,
			ID:         buildID,
		})
		if cancelErr != nil {
			return fmt.Errorf("cancel superseded builds: %w", cancelErr)
		}
		notify := map[uuid.UUID]struct{}{clusterID: {}}
		for _, row := range superseded {
			supersededSources = append(supersededSources, buildSource{id: row.ID, key: row.SourceKey})
			if row.ClusterID != nil {
				notify[*row.ClusterID] = struct{}{}
			}
		}

		rows, queueErr := qtx.QueueBuild(ctx, genDb.QueueBuildParams{
			ClusterID: &clusterID,
			Message:   "queued",
			ID:        buildID,
		})
		if queueErr != nil {
			return fmt.Errorf("queue build: %w", queueErr)
		}
		if rows == 0 {
			return errBuildNotQueueable
		}
		for id := range notify {
			if notifyErr := notifyCluster(ctx, qtx, id); notifyErr != nil {
				return notifyErr
			}
		}
		supersededIDs := make([]string, len(supersededSources))
		for i, source := range supersededSources {
			supersededIDs[i] = source.id.String()
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.BuildStarted,
			ResourceID:  new(resourceID),
			SubjectType: events.SubjectBuild,
			SubjectID:   new(buildID),
			Data: map[string]any{
				events.FieldResourceID: resourceID.String(),
				events.FieldClusterID:  clusterID.String(),
				events.FieldSuperseded: supersededIDs,
			},
		})
	})
	if err != nil {
		return nil, err
	}
	return supersededSources, nil
}

type buildSource struct {
	id  uuid.UUID
	key string
}

func deleteBuildSources(ctx context.Context, queries genDb.Querier, bucket SourceBucket, sources ...buildSource) {
	if bucket == nil {
		return
	}
	for _, source := range sources {
		if err := buildsource.Delete(ctx, queries, bucket, source.id, source.key); err != nil {
			slog.WarnContext(ctx, "failed to delete build source", "buildId", source.id, "error", err)
		}
	}
}

func (s *BuildServer) GetBuild(
	ctx context.Context,
	req *buildv1.GetBuildRequest,
) (*buildv1.GetBuildResponse, error) {
	rawBuildID := req.GetBuildId()
	build, err := s.getBuild(ctx, rawBuildID)
	if err != nil {
		return nil, err
	}

	if authErr := authorize(ctx, s.authz, actions.GetBuild, build.ResourceID); authErr != nil {
		return nil, authErr
	}

	buildProto := buildToProto(build)
	return &buildv1.GetBuildResponse{Build: buildProto}, nil
}

func (s *BuildServer) ListBuilds(
	ctx context.Context,
	req *buildv1.ListBuildsRequest,
) (*buildv1.ListBuildsResponse, error) {
	resourceID, err := parseID("resource_id", req.GetResourceId())
	if err != nil {
		return nil, err
	}

	if authErr := authorize(ctx, s.authz, actions.ListBuilds, resourceID); authErr != nil {
		return nil, authErr
	}

	requestedPageSize := req.GetPageSize()
	pageSize := normalizePageSize(requestedPageSize)

	cursor, cursorErr := decodePositionCursor(req.GetPageToken())
	if cursorErr != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(cursorErr)
	}

	rows, err := s.queries.ListBuildsForResource(ctx, genDb.ListBuildsForResourceParams{
		ResourceID:    resourceID,
		Limit:         pageSize,
		PageToken:     cursor.ID,
		PageCreatedAt: cursor.CreatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("list builds of resource %s: %w", resourceID, err)
	}

	builds := make([]*buildv1.Build, 0, len(rows))
	for _, b := range rows {
		item := buildToProto(b)
		builds = append(builds, item)
	}

	token := nextPageToken(rows, pageSize, func(b genDb.Build) (uuid.UUID, time.Time) { return b.ID, b.CreatedAt })
	return &buildv1.ListBuildsResponse{
		Builds:        builds,
		NextPageToken: token,
	}, nil
}

func (s *BuildServer) CancelBuild(
	ctx context.Context,
	req *buildv1.CancelBuildRequest,
) (*buildv1.CancelBuildResponse, error) {
	rawBuildID := req.GetBuildId()
	build, err := s.getBuild(ctx, rawBuildID)
	if err != nil {
		return nil, err
	}

	if authErr := authorize(ctx, s.authz, actions.CancelBuild, build.ResourceID); authErr != nil {
		return nil, authErr
	}

	if cancelErr := cancelBuild(ctx, s.db, s.queries, s.bucket, build.ID, build.ResourceID); cancelErr != nil {
		return nil, fmt.Errorf("cancel build %s: %w", build.ID, cancelErr)
	}

	canceled, err := s.getBuild(ctx, rawBuildID)
	if err != nil {
		return nil, err
	}
	canceledProto := buildToProto(canceled)
	return &buildv1.CancelBuildResponse{Build: canceledProto}, nil
}

func cancelBuild(
	ctx context.Context,
	pool *pgxpool.Pool,
	queries genDb.Querier,
	bucket SourceBucket,
	buildID, resourceID uuid.UUID,
) error {
	var source buildSource
	err := withTx(ctx, pool, func(qtx *genDb.Queries) error {
		row, cancelErr := qtx.CancelBuild(ctx, genDb.CancelBuildParams{
			Message: "canceled",
			ID:      buildID,
		})
		if errors.Is(cancelErr, pgx.ErrNoRows) {
			return errBuildNotCancelable
		}
		if cancelErr != nil {
			return fmt.Errorf("cancel build: %w", cancelErr)
		}
		source = buildSource{id: row.ID, key: row.SourceKey}
		if row.ClusterID != nil {
			if notifyErr := notifyCluster(ctx, qtx, *row.ClusterID); notifyErr != nil {
				return notifyErr
			}
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.BuildCanceled,
			ResourceID:  new(resourceID),
			SubjectType: events.SubjectBuild,
			SubjectID:   new(buildID),
			Data:        map[string]any{events.FieldResourceID: resourceID.String()},
		})
	})
	if err != nil {
		return err
	}
	deleteBuildSources(ctx, queries, bucket, source)
	return nil
}

func (s *BuildServer) getBuild(ctx context.Context, rawID string) (genDb.Build, error) {
	buildID, err := uuid.Parse(rawID)
	if err != nil {
		invalid := fmt.Errorf("invalid build_id: %w", err)
		return genDb.Build{}, connect.NewError(connect.CodeInvalidArgument, publicInvalidBuildID).WithCause(invalid)
	}
	build, err := s.queries.GetBuildByID(ctx, buildID)
	if errors.Is(err, pgx.ErrNoRows) {
		return genDb.Build{}, ErrBuildNotFound
	}
	if err != nil {
		return genDb.Build{}, fmt.Errorf("get build %s: %w", buildID, err)
	}
	return build, nil
}

func buildStatusToProto(status genDb.BuildStatus) buildv1.BuildStatus {
	switch status {
	case genDb.BuildStatusAwaitingUpload:
		return buildv1.BuildStatus_BUILD_STATUS_AWAITING_UPLOAD
	case genDb.BuildStatusQueued:
		return buildv1.BuildStatus_BUILD_STATUS_QUEUED
	case genDb.BuildStatusRunning:
		return buildv1.BuildStatus_BUILD_STATUS_RUNNING
	case genDb.BuildStatusSucceeded:
		return buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED
	case genDb.BuildStatusFailed:
		return buildv1.BuildStatus_BUILD_STATUS_FAILED
	case genDb.BuildStatusCanceled:
		return buildv1.BuildStatus_BUILD_STATUS_CANCELED
	default:
		return buildv1.BuildStatus_BUILD_STATUS_UNSPECIFIED
	}
}

func buildToProto(b genDb.Build) *buildv1.Build {
	id := b.ID.String()
	resourceID := b.ResourceID.String()
	status := buildStatusToProto(b.Status)
	createdBy := b.CreatedBy.String()
	createdAt := timestamppb.New(b.CreatedAt)
	startedAt := optionalTimestamp(b.StartedAt)
	finishedAt := optionalTimestamp(b.FinishedAt)
	imageDeletedAt := optionalTimestamp(b.ImageDeletedAt)

	out := &buildv1.Build{
		Id:               id,
		ResourceId:       resourceID,
		Status:           status,
		SourceType:       b.SourceType,
		SourceKey:        b.SourceKey,
		SourceSize:       b.SourceSize,
		DockerfilePath:   b.DockerfilePath,
		Context:          b.Context,
		ImageRepository:  b.ImageRepository,
		ImageDigest:      b.ImageDigest,
		Message:          b.Message,
		CreatedBy:        createdBy,
		CreatedAt:        createdAt,
		StartedAt:        startedAt,
		FinishedAt:       finishedAt,
		ImageDeletedAt:   imageDeletedAt,
		GitCommit:        b.GitCommit,
		GitBranch:        b.GitBranch,
		GitCommitMessage: b.GitCommitMessage,
	}
	if b.ClusterID != nil {
		clusterID := b.ClusterID.String()
		out.ClusterId = &clusterID
	}
	return out
}
