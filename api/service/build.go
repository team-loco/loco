package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/sourcebucket"
	timeutil "github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/actions"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	buildSourceTypeUpload = "upload"
	buildUploadURLTTL     = 15 * time.Minute
	buildSourcePrefix     = "sources/"
)

var (
	ErrBuildNotFound    = errors.New("build not found")
	ErrBuildsNotEnabled = errors.New(
		"builds are not enabled on this server: the source bucket and registry are not configured",
	)
	errBuildNotQueueable   = errors.New("build is no longer awaiting upload")
	errBuildNotCancelable  = errors.New("build has already finished")
	errEntityScopesMissing = errors.New("entity scopes not found in context")
	errSourceNotUploaded   = errors.New("the source has not been uploaded")
	errSourceCheckFailed   = errors.New("failed to check the uploaded source")
	errUploadURLFailed     = errors.New("failed to create the source upload url")
	errNoBuildCluster      = errors.New("no cluster in this install accepts builds")
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
	machine *tvm.VendingMachine
	bucket  SourceBucket
	config  BuildConfig
	now     func() time.Time
}

func NewBuildServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	machine *tvm.VendingMachine,
	bucket SourceBucket,
	config BuildConfig,
) *BuildServer {
	return &BuildServer{
		db:      db,
		queries: queries,
		machine: machine,
		bucket:  bucket,
		config:  config,
		now:     time.Now,
	}
}

func (s *BuildServer) enabled() bool {
	return s.bucket != nil && s.config.RegistryHost != ""
}

func buildsUnavailable(reason error) *connect.Error {
	connectErr := connect.NewError(connect.CodeFailedPrecondition, reason)
	detail, err := connect.NewErrorDetail(&buildv1.BuildsUnavailable{})
	if err != nil {
		slog.Error("failed to attach the builds unavailable detail", "error", err)
		return connectErr
	}
	connectErr.AddDetail(detail)
	return connectErr
}

func (s *BuildServer) authorize(ctx context.Context, action actions.Action, resourceID uuid.UUID) error {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return connect.NewError(connect.CodeInternal, errEntityScopesMissing)
	}
	resourceIDStr := resourceID.String()
	scope := actions.New(action, resourceIDStr)
	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, scope); err != nil {
		slog.WarnContext(ctx, "unauthorized build request", "resourceId", resourceIDStr)
		return connect.NewError(connect.CodePermissionDenied, err)
	}
	return nil
}

func imageRepository(registryHost, registryPrefix string, workspaceID, resourceID uuid.UUID) string {
	host := strings.TrimSuffix(registryHost, "/")
	parts := []string{host}
	prefix := strings.Trim(registryPrefix, "/")
	if prefix != "" {
		parts = append(parts, prefix)
	}
	workspacePath := "ws-" + workspaceID.String()
	resourcePath := resourceID.String()
	parts = append(parts, workspacePath, resourcePath)
	return strings.Join(parts, "/")
}

func buildSourceKey(buildID uuid.UUID) string {
	return buildSourcePrefix + buildID.String() + ".tar.gz"
}

func (s *BuildServer) CreateBuild(
	ctx context.Context,
	req *connect.Request[buildv1.CreateBuildRequest],
) (*connect.Response[buildv1.CreateBuildResponse], error) {
	r := req.Msg

	if !s.enabled() {
		return nil, buildsUnavailable(ErrBuildsNotEnabled)
	}

	rawResourceID := r.GetResourceId()
	resourceID, err := uuid.Parse(rawResourceID)
	if err != nil {
		invalid := fmt.Errorf("invalid resource_id: %w", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, invalid)
	}

	if authErr := s.authorize(ctx, actions.CreateBuild, resourceID); authErr != nil {
		return nil, authErr
	}

	if _, clusterErr := s.buildCluster(ctx); clusterErr != nil {
		return nil, clusterErr
	}

	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	sourceSize := r.GetSourceSize()
	if sourceSize > s.config.SourceMaxBytes {
		tooLarge := fmt.Errorf("source is %d bytes, over the %d byte limit", sourceSize, s.config.SourceMaxBytes)
		return nil, connect.NewError(connect.CodeInvalidArgument, tooLarge)
	}

	resource, err := s.queries.GetResourceByID(ctx, resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource", "error", err, "resourceId", resourceID)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	buildID, err := uuid.NewV7()
	if err != nil {
		slog.ErrorContext(ctx, "failed to generate build id", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	sourceKey := buildSourceKey(buildID)

	issuedAt := s.now()
	uploadURL, err := s.bucket.PresignPut(ctx, sourceKey, sourceSize, buildUploadURLTTL)
	if err != nil {
		slog.ErrorContext(ctx, "failed to presign source upload", "error", err, "buildId", buildID)
		return nil, connect.NewError(connect.CodeInternal, errUploadURLFailed)
	}

	repository := imageRepository(s.config.RegistryHost, s.config.RegistryPrefix, resource.WorkspaceID, resource.ID)
	dockerfilePath := r.GetDockerfilePath()
	if _, createErr := s.queries.CreateBuild(ctx, genDb.CreateBuildParams{
		ID:              buildID,
		ResourceID:      resource.ID,
		SourceType:      buildSourceTypeUpload,
		SourceKey:       sourceKey,
		SourceSize:      sourceSize,
		DockerfilePath:  dockerfilePath,
		ImageRepository: repository,
		CreatedBy:       entity.ID,
	}); createErr != nil {
		slog.ErrorContext(ctx, "failed to create build", "error", createErr, "resourceId", resource.ID)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	expiresAt := issuedAt.Add(buildUploadURLTTL)
	uploadExpiresAt := timestamppb.New(expiresAt)
	buildIDStr := buildID.String()
	return connect.NewResponse(&buildv1.CreateBuildResponse{
		BuildId:         buildIDStr,
		UploadUrl:       uploadURL,
		UploadExpiresAt: uploadExpiresAt,
	}), nil
}

func (s *BuildServer) StartBuild(
	ctx context.Context,
	req *connect.Request[buildv1.StartBuildRequest],
) (*connect.Response[buildv1.StartBuildResponse], error) {
	r := req.Msg

	if !s.enabled() {
		return nil, buildsUnavailable(ErrBuildsNotEnabled)
	}

	rawBuildID := r.GetBuildId()
	build, err := s.getBuild(ctx, rawBuildID)
	if err != nil {
		return nil, err
	}

	if authErr := s.authorize(ctx, actions.CreateBuild, build.ResourceID); authErr != nil {
		return nil, authErr
	}

	if build.Status != genDb.BuildStatusAwaitingUpload {
		notAwaiting := fmt.Errorf("build is %s, not awaiting upload", build.Status)
		return nil, connect.NewError(connect.CodeFailedPrecondition, notAwaiting)
	}

	size, err := s.bucket.ObjectSize(ctx, build.SourceKey)
	if errors.Is(err, sourcebucket.ErrObjectNotFound) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errSourceNotUploaded)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to check uploaded source", "error", err, "buildId", build.ID)
		return nil, connect.NewError(connect.CodeInternal, errSourceCheckFailed)
	}
	if size != build.SourceSize {
		mismatch := fmt.Errorf("the uploaded source is %d bytes, expected %d", size, build.SourceSize)
		return nil, connect.NewError(connect.CodeFailedPrecondition, mismatch)
	}

	clusterID, err := s.buildCluster(ctx)
	if err != nil {
		return nil, err
	}

	superseded, txErr := queueBuild(ctx, s.db, build.ID, build.ResourceID, clusterID)
	if txErr != nil {
		if errors.Is(txErr, errBuildNotQueueable) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, txErr)
		}
		slog.ErrorContext(ctx, "failed to queue build", "error", txErr, "buildId", build.ID)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	deleteBuildSources(ctx, s.queries, s.bucket, superseded...)

	queued, err := s.getBuild(ctx, rawBuildID)
	if err != nil {
		return nil, err
	}
	queuedProto := buildToProto(queued)
	return connect.NewResponse(&buildv1.StartBuildResponse{Build: queuedProto}), nil
}

func (s *BuildServer) buildCluster(ctx context.Context) (uuid.UUID, error) {
	clusterID, err := s.queries.GetBuildCluster(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, buildsUnavailable(errNoBuildCluster)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get build cluster", "error", err)
		return uuid.UUID{}, connect.NewError(connect.CodeInternal, ErrDB)
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
		if _, lockErr := qtx.LockResourceForBuild(ctx, resourceID); lockErr != nil {
			return fmt.Errorf("lock resource: %w", lockErr)
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
		return nil
	})
	if err != nil {
		return nil, err
	}
	return supersededSources, nil
}

func notifyCluster(ctx context.Context, qtx *genDb.Queries, clusterID uuid.UUID) error {
	clusterIDText := clusterID.String()
	if err := qtx.NotifyClusterPlacements(ctx, clusterIDText); err != nil {
		return fmt.Errorf("notify cluster %s: %w", clusterID, err)
	}
	return nil
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
		if err := deleteBuildSource(ctx, queries, bucket, source.id, source.key); err != nil {
			slog.WarnContext(ctx, "failed to delete build source", "buildId", source.id, "error", err)
		}
	}
}

func (s *BuildServer) GetBuild(
	ctx context.Context,
	req *connect.Request[buildv1.GetBuildRequest],
) (*connect.Response[buildv1.GetBuildResponse], error) {
	rawBuildID := req.Msg.GetBuildId()
	build, err := s.getBuild(ctx, rawBuildID)
	if err != nil {
		return nil, err
	}

	if authErr := s.authorize(ctx, actions.GetBuild, build.ResourceID); authErr != nil {
		return nil, authErr
	}

	buildProto := buildToProto(build)
	return connect.NewResponse(&buildv1.GetBuildResponse{Build: buildProto}), nil
}

func (s *BuildServer) ListBuilds(
	ctx context.Context,
	req *connect.Request[buildv1.ListBuildsRequest],
) (*connect.Response[buildv1.ListBuildsResponse], error) {
	r := req.Msg

	rawResourceID := r.GetResourceId()
	resourceID, err := uuid.Parse(rawResourceID)
	if err != nil {
		invalid := fmt.Errorf("invalid resource_id: %w", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, invalid)
	}

	if authErr := s.authorize(ctx, actions.ListBuilds, resourceID); authErr != nil {
		return nil, authErr
	}

	requestedPageSize := r.GetPageSize()
	pageSize := normalizePageSize(requestedPageSize)

	var pageToken *string
	if rawToken := r.GetPageToken(); rawToken != "" {
		cursorID, decodeErr := decodeCursor(rawToken)
		if decodeErr != nil {
			invalid := fmt.Errorf("invalid page_token: %w", decodeErr)
			return nil, connect.NewError(connect.CodeInvalidArgument, invalid)
		}
		pageToken = &cursorID
	}

	rows, err := s.queries.ListBuildsForResource(ctx, genDb.ListBuildsForResourceParams{
		ResourceID: resourceID,
		Limit:      pageSize,
		PageToken:  pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list builds", "error", err, "resourceId", resourceID)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	builds := make([]*buildv1.Build, 0, len(rows))
	for _, b := range rows {
		item := buildToProto(b)
		builds = append(builds, item)
	}

	var nextPageToken string
	if len(rows) == int(pageSize) {
		lastID := rows[len(rows)-1].ID.String()
		nextPageToken = encodeCursor(lastID)
	}

	return connect.NewResponse(&buildv1.ListBuildsResponse{
		Builds:        builds,
		NextPageToken: nextPageToken,
	}), nil
}

func (s *BuildServer) CancelBuild(
	ctx context.Context,
	req *connect.Request[buildv1.CancelBuildRequest],
) (*connect.Response[buildv1.CancelBuildResponse], error) {
	rawBuildID := req.Msg.GetBuildId()
	build, err := s.getBuild(ctx, rawBuildID)
	if err != nil {
		return nil, err
	}

	if authErr := s.authorize(ctx, actions.CancelBuild, build.ResourceID); authErr != nil {
		return nil, authErr
	}

	if cancelErr := cancelBuild(ctx, s.db, s.queries, s.bucket, build.ID); cancelErr != nil {
		if errors.Is(cancelErr, errBuildNotCancelable) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, cancelErr)
		}
		slog.ErrorContext(ctx, "failed to cancel build", "error", cancelErr, "buildId", build.ID)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	canceled, err := s.getBuild(ctx, rawBuildID)
	if err != nil {
		return nil, err
	}
	canceledProto := buildToProto(canceled)
	return connect.NewResponse(&buildv1.CancelBuildResponse{Build: canceledProto}), nil
}

func cancelBuild(
	ctx context.Context,
	pool *pgxpool.Pool,
	queries genDb.Querier,
	bucket SourceBucket,
	buildID uuid.UUID,
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
		if row.ClusterID == nil {
			return nil
		}
		return notifyCluster(ctx, qtx, *row.ClusterID)
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
		return genDb.Build{}, connect.NewError(connect.CodeInvalidArgument, invalid)
	}
	build, err := s.queries.GetBuildByID(ctx, buildID)
	if errors.Is(err, pgx.ErrNoRows) {
		return genDb.Build{}, connect.NewError(connect.CodeNotFound, ErrBuildNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get build", "error", err, "buildId", buildID)
		return genDb.Build{}, connect.NewError(connect.CodeInternal, ErrDB)
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
	createdAt := timeutil.ParsePostgresTimestamp(b.CreatedAt)
	startedAt := timeutil.ParsePostgresTimestampPtr(b.StartedAt)
	finishedAt := timeutil.ParsePostgresTimestampPtr(b.FinishedAt)

	out := &buildv1.Build{
		Id:              id,
		ResourceId:      resourceID,
		Status:          status,
		SourceType:      b.SourceType,
		SourceKey:       b.SourceKey,
		SourceSize:      b.SourceSize,
		DockerfilePath:  b.DockerfilePath,
		ImageRepository: b.ImageRepository,
		ImageDigest:     b.ImageDigest,
		Message:         b.Message,
		CreatedBy:       createdBy,
		CreatedAt:       createdAt,
		StartedAt:       startedAt,
		FinishedAt:      finishedAt,
	}
	if b.ClusterID != nil {
		clusterID := b.ClusterID.String()
		out.ClusterId = &clusterID
	}
	return out
}
