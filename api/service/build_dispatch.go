package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const (
	buildSourceURLTTL         = time.Hour
	buildMissingMessage       = "the cluster no longer has this build"
	buildFailedMessage        = "the build failed"
	buildCanceledMessage      = "the build was canceled on the cluster"
	buildRunningMessage       = "running"
	buildSucceededMessage     = "succeeded"
	buildMissingDigestMessage = "the build reported success without an image digest"
	buildsDisabledMessage     = "builds are not enabled on the cluster this build was queued on"
)

type buildTracker struct {
	started  map[uuid.UUID]struct{}
	active   map[uuid.UUID]struct{}
	canceled map[uuid.UUID]struct{}
}

func newBuildTracker() buildTracker {
	return buildTracker{
		started:  make(map[uuid.UUID]struct{}),
		active:   make(map[uuid.UUID]struct{}),
		canceled: make(map[uuid.UUID]struct{}),
	}
}

func buildPhaseFinished(phase agentv1.BuildPhase) bool {
	switch phase {
	case agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED:
		return true
	case agentv1.BuildPhase_BUILD_PHASE_FAILED:
		return true
	case agentv1.BuildPhase_BUILD_PHASE_CANCELED:
		return true
	default:
		return false
	}
}

func buildStatusFinished(status genDb.BuildStatus) bool {
	switch status {
	case genDb.BuildStatusSucceeded:
		return true
	case genDb.BuildStatusFailed:
		return true
	case genDb.BuildStatusCanceled:
		return true
	default:
		return false
	}
}

func sortedIDs(set map[uuid.UUID]struct{}) []uuid.UUID {
	keys := maps.Keys(set)
	ids := slices.Collect(keys)
	slices.SortFunc(ids, func(a, b uuid.UUID) int {
		return bytes.Compare(a[:], b[:])
	})
	return ids
}

func (ss *syncSession) reconcileBuilds(ctx context.Context) error {
	if err := ss.startQueuedBuilds(ctx); err != nil {
		return err
	}
	return ss.cancelStaleBuilds(ctx)
}

func (ss *syncSession) startQueuedBuilds(ctx context.Context) error {
	if ss.server.sources == nil {
		return nil
	}
	clusterID := ss.clusterID
	buildsEnabled, err := ss.server.queries.GetClusterBuildsEnabled(ctx, clusterID)
	if err != nil {
		slog.ErrorContext(
			ctx,
			"failed to check whether the cluster accepts builds",
			"cluster_id",
			clusterID,
			"error",
			err,
		)
		return nil
	}
	if !buildsEnabled {
		ss.server.failQueuedBuilds(ctx, clusterID)
		return nil
	}
	queued, err := ss.server.queries.ListQueuedClusterBuilds(ctx, &clusterID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list queued builds", "cluster_id", ss.clusterID, "error", err)
		return nil
	}
	for _, build := range queued {
		if _, sent := ss.builds.started[build.ID]; sent {
			continue
		}
		if _, held := ss.builds.active[build.ID]; held {
			continue
		}
		msg, msgErr := ss.server.startBuildMessage(ctx, build)
		if msgErr != nil {
			slog.ErrorContext(ctx, "failed to prepare build for the cluster",
				"cluster_id", ss.clusterID,
				"build_id", build.ID,
				"error", msgErr,
			)
			continue
		}
		if sendErr := ss.sendFn(msg); sendErr != nil {
			return fmt.Errorf("send build %s: %w", build.ID, sendErr)
		}
		ss.builds.started[build.ID] = struct{}{}
		ss.builds.active[build.ID] = struct{}{}
		slog.InfoContext(ctx, "build sent to cluster",
			"cluster_id", ss.clusterID,
			"build_id", build.ID,
			"cache_ref", build.CacheRef,
		)
	}
	return nil
}

func (s *AgentServer) failQueuedBuilds(ctx context.Context, clusterID uuid.UUID) {
	failed, err := s.queries.FailQueuedClusterBuilds(ctx, genDb.FailQueuedClusterBuildsParams{
		Message:   buildsDisabledMessage,
		ClusterID: &clusterID,
	})
	if err != nil {
		slog.ErrorContext(
			ctx,
			"failed to fail builds queued on a cluster without builds",
			"cluster_id",
			clusterID,
			"error",
			err,
		)
		return
	}
	sources := make([]buildSource, 0, len(failed))
	for _, row := range failed {
		slog.WarnContext(ctx, "build queued on a cluster without builds; marked failed",
			"cluster_id", clusterID,
			"build_id", row.ID,
		)
		sources = append(sources, buildSource{id: row.ID, key: row.SourceKey})
	}
	deleteBuildSources(ctx, s.queries, s.sources, sources...)
}

func (s *AgentServer) startBuildMessage(
	ctx context.Context,
	build genDb.ListQueuedClusterBuildsRow,
) (*agentv1.SyncResponse, error) {
	sourceURL, err := s.sources.PresignGet(ctx, build.SourceKey, buildSourceURLTTL)
	if err != nil {
		return nil, fmt.Errorf("presign source: %w", err)
	}
	buildID := build.ID.String()
	workspaceID := build.WorkspaceID.String()
	resourceID := build.ResourceID.String()
	return &agentv1.SyncResponse{
		Message: &agentv1.SyncResponse_StartBuild{
			StartBuild: &agentv1.StartBuild{
				BuildId:         buildID,
				WorkspaceId:     workspaceID,
				ResourceId:      resourceID,
				SourceUrl:       sourceURL,
				DockerfilePath:  build.DockerfilePath,
				ImageRepository: build.ImageRepository,
				CacheRef:        build.CacheRef,
			},
		},
	}, nil
}

func (ss *syncSession) cancelStaleBuilds(ctx context.Context) error {
	pending := make(map[uuid.UUID]struct{}, len(ss.builds.active))
	for id := range ss.builds.active {
		if _, sent := ss.builds.canceled[id]; !sent {
			pending[id] = struct{}{}
		}
	}
	if len(pending) == 0 {
		return nil
	}

	ids := sortedIDs(pending)
	rows, err := ss.server.queries.ListBuildStatesByIDs(ctx, ids)
	if err != nil {
		slog.ErrorContext(ctx, "failed to load builds held by the cluster", "cluster_id", ss.clusterID, "error", err)
		return nil
	}
	states := make(map[uuid.UUID]genDb.ListBuildStatesByIDsRow, len(rows))
	for _, row := range rows {
		states[row.ID] = row
	}

	for _, id := range ids {
		row, known := states[id]
		if known && ss.wantsBuild(row) {
			continue
		}
		buildID := id.String()
		msg := &agentv1.SyncResponse{
			Message: &agentv1.SyncResponse_CancelBuild{
				CancelBuild: &agentv1.CancelBuild{BuildId: buildID},
			},
		}
		if sendErr := ss.sendFn(msg); sendErr != nil {
			return fmt.Errorf("send build cancel %s: %w", id, sendErr)
		}
		ss.builds.canceled[id] = struct{}{}
		slog.InfoContext(ctx, "build cancel sent to cluster", "cluster_id", ss.clusterID, "build_id", id)
	}
	return nil
}

func (ss *syncSession) wantsBuild(row genDb.ListBuildStatesByIDsRow) bool {
	if row.ClusterID == nil || *row.ClusterID != ss.clusterID {
		return false
	}
	return !buildStatusFinished(row.Status)
}

func (ss *syncSession) reconcileBuildInventory(ctx context.Context, builds []*agentv1.InventoryBuild) error {
	held := make([]uuid.UUID, 0, len(builds))
	heldSet := make(map[uuid.UUID]struct{}, len(builds))
	active := make(map[uuid.UUID]struct{}, len(builds))
	for _, build := range builds {
		if err := protovalidate.Validate(build); err != nil {
			slog.WarnContext(ctx, "ignoring invalid build inventory entry",
				"cluster_id", ss.clusterID,
				"build_id", build.GetBuildId(),
				"error", err,
			)
			continue
		}
		rawID := build.GetBuildId()
		id, err := uuid.Parse(rawID)
		if err != nil {
			continue
		}
		held = append(held, id)
		heldSet[id] = struct{}{}
		phase := build.GetPhase()
		if !buildPhaseFinished(phase) {
			active[id] = struct{}{}
		}
	}

	ss.builds.active = active
	ss.builds.canceled = make(map[uuid.UUID]struct{})
	for id := range ss.builds.started {
		if _, ok := heldSet[id]; !ok {
			delete(ss.builds.started, id)
		}
	}

	ss.server.failMissingBuilds(ctx, ss.clusterID, held)
	return ss.reconcileBuilds(ctx)
}

func (s *AgentServer) failMissingBuilds(ctx context.Context, clusterID uuid.UUID, held []uuid.UUID) {
	failed, err := s.queries.FailMissingClusterBuilds(ctx, genDb.FailMissingClusterBuildsParams{
		Message:   buildMissingMessage,
		ClusterID: &clusterID,
		Held:      held,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to fail builds missing from the cluster", "cluster_id", clusterID, "error", err)
		return
	}
	sources := make([]buildSource, 0, len(failed))
	for _, row := range failed {
		slog.WarnContext(ctx, "running build missing from the cluster; marked failed",
			"cluster_id", clusterID,
			"build_id", row.ID,
		)
		sources = append(sources, buildSource{id: row.ID, key: row.SourceKey})
	}
	deleteBuildSources(ctx, s.queries, s.sources, sources...)
}

func (ss *syncSession) handleBuildStatus(ctx context.Context, status *agentv1.BuildStatus) error {
	if err := protovalidate.Validate(status); err != nil {
		slog.WarnContext(ctx, "ignoring invalid build status",
			"cluster_id", ss.clusterID,
			"build_id", status.GetBuildId(),
			"error", err,
		)
		return nil
	}
	rawID := status.GetBuildId()
	id, err := uuid.Parse(rawID)
	if err != nil {
		slog.WarnContext(ctx, "ignoring build status with an invalid build id",
			"cluster_id", ss.clusterID,
			"build_id", rawID,
			"error", err,
		)
		return nil
	}
	if err := ss.server.recordBuildStatus(ctx, ss.clusterID, id, status); err != nil {
		return err
	}
	phase := status.GetPhase()
	if buildPhaseFinished(phase) {
		delete(ss.builds.active, id)
	} else {
		ss.builds.active[id] = struct{}{}
	}
	return ss.cancelStaleBuilds(ctx)
}

func (s *AgentServer) recordBuildStatus(
	ctx context.Context,
	clusterID, buildID uuid.UUID,
	status *agentv1.BuildStatus,
) error {
	var err error
	reported := status.GetMessage()
	switch status.GetPhase() {
	case agentv1.BuildPhase_BUILD_PHASE_PENDING:
		err = s.recordBuildPending(ctx, clusterID, buildID, status)
	case agentv1.BuildPhase_BUILD_PHASE_RUNNING:
		err = s.recordBuildRunning(ctx, clusterID, buildID, status)
	case agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED:
		err = s.recordBuildSucceeded(ctx, clusterID, buildID, status)
	case agentv1.BuildPhase_BUILD_PHASE_FAILED:
		message := messageOr(reported, buildFailedMessage)
		err = s.finishBuild(ctx, clusterID, buildID, genDb.BuildStatusFailed, message, nil, nil)
	case agentv1.BuildPhase_BUILD_PHASE_CANCELED:
		message := messageOr(reported, buildCanceledMessage)
		err = s.finishBuild(ctx, clusterID, buildID, genDb.BuildStatusCanceled, message, nil, nil)
	case agentv1.BuildPhase_BUILD_PHASE_UNSPECIFIED:
		return nil
	default:
		return nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to record build status",
			"cluster_id", clusterID,
			"build_id", buildID,
			"phase", status.GetPhase(),
			"error", err,
		)
		return fmt.Errorf("record build %s status: %w", buildID, err)
	}
	return nil
}

func messageOr(message, fallback string) string {
	if message == "" {
		return fallback
	}
	return message
}

func (s *AgentServer) recordBuildPending(
	ctx context.Context,
	clusterID, buildID uuid.UUID,
	status *agentv1.BuildStatus,
) error {
	message := status.GetMessage()
	if message == "" {
		return nil
	}
	if _, err := s.queries.UpdateQueuedBuildMessage(ctx, genDb.UpdateQueuedBuildMessageParams{
		Message:   message,
		ID:        buildID,
		ClusterID: &clusterID,
	}); err != nil {
		return fmt.Errorf("update queued build: %w", err)
	}
	return nil
}

func (s *AgentServer) recordBuildRunning(
	ctx context.Context,
	clusterID, buildID uuid.UUID,
	status *agentv1.BuildStatus,
) error {
	reported := status.GetMessage()
	message := messageOr(reported, buildRunningMessage)
	rows, err := s.queries.MarkBuildRunning(ctx, genDb.MarkBuildRunningParams{
		Message:   message,
		ID:        buildID,
		ClusterID: &clusterID,
	})
	if err != nil {
		return fmt.Errorf("mark build running: %w", err)
	}
	if rows > 0 {
		slog.InfoContext(ctx, "build running", "cluster_id", clusterID, "build_id", buildID)
	}
	return nil
}

func (s *AgentServer) recordBuildSucceeded(
	ctx context.Context,
	clusterID, buildID uuid.UUID,
	status *agentv1.BuildStatus,
) error {
	imageDigest := status.GetImageDigest()
	if imageDigest == "" {
		return s.finishBuild(ctx, clusterID, buildID, genDb.BuildStatusFailed, buildMissingDigestMessage, nil, nil)
	}
	var cacheDigest *string
	if digest := status.GetCacheDigest(); digest != "" {
		cacheDigest = &digest
	}
	reported := status.GetMessage()
	message := messageOr(reported, buildSucceededMessage)
	return s.finishBuild(ctx, clusterID, buildID, genDb.BuildStatusSucceeded, message, &imageDigest, cacheDigest)
}

func (s *AgentServer) finishBuild(
	ctx context.Context,
	clusterID, buildID uuid.UUID,
	status genDb.BuildStatus,
	message string,
	imageDigest, cacheDigest *string,
) error {
	row, err := s.queries.FinishBuild(ctx, genDb.FinishBuildParams{
		Status:      status,
		ImageDigest: imageDigest,
		CacheDigest: cacheDigest,
		Message:     message,
		ID:          buildID,
		ClusterID:   &clusterID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("finish build: %w", err)
	}
	slog.InfoContext(ctx, "build finished",
		"cluster_id", clusterID,
		"build_id", buildID,
		"status", status,
	)
	finished := buildSource{id: row.ID, key: row.SourceKey}
	deleteBuildSources(ctx, s.queries, s.sources, finished)
	return nil
}
