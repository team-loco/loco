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
	"github.com/team-loco/loco/api/events"
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

var errBuildLeftQueue = errors.New("the build is no longer queued on this cluster")

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
		return fmt.Errorf("check whether the cluster accepts builds: %w", err)
	}
	if !buildsEnabled {
		return ss.server.failClusterBuilds(ctx, genDb.FailClusterBuildsParams{
			ClusterID:      &clusterID,
			PreviousStatus: genDb.BuildStatusQueued,
			Held:           []uuid.UUID{},
			Message:        buildsDisabledMessage,
		})
	}
	queued, err := ss.server.queries.ListQueuedClusterBuilds(ctx, &clusterID)
	if err != nil {
		return fmt.Errorf("list queued builds: %w", err)
	}
	for _, build := range queued {
		if _, sent := ss.builds.started[build.ID]; sent {
			continue
		}
		if _, held := ss.builds.active[build.ID]; held {
			continue
		}
		msg, msgErr := ss.server.startBuildMessage(ctx, clusterID, build)
		if errors.Is(msgErr, errBuildLeftQueue) {
			slog.InfoContext(ctx, "build left the queue before it was sent",
				"cluster_id", ss.clusterID,
				"build_id", build.ID,
			)
			continue
		}
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

func (s *AgentServer) startBuildMessage(
	ctx context.Context,
	clusterID uuid.UUID,
	build genDb.ListQueuedClusterBuildsRow,
) (*agentv1.SyncResponse, error) {
	sourceURL, err := s.sources.PresignGet(ctx, build.SourceKey, buildSourceURLTTL)
	if err != nil {
		return nil, fmt.Errorf("presign source: %w", err)
	}
	buildID := build.ID.String()
	workspaceID := build.WorkspaceID.String()
	organizationID := build.OrgID.String()
	resourceID := build.ResourceID.String()
	registryCredential, err := s.buildRegistryCredential(ctx, clusterID, build.ID)
	if err != nil {
		return nil, err
	}
	return &agentv1.SyncResponse{
		Message: &agentv1.SyncResponse_StartBuild{
			StartBuild: &agentv1.StartBuild{
				BuildId:            buildID,
				OrganizationId:     organizationID,
				WorkspaceId:        workspaceID,
				ResourceId:         resourceID,
				SourceUrl:          sourceURL,
				DockerfilePath:     build.DockerfilePath,
				Context:            build.Context,
				ImageRepository:    build.ImageRepository,
				CacheRef:           build.CacheRef,
				RegistryCredential: registryCredential,
			},
		},
	}, nil
}

func (s *AgentServer) buildRegistryCredential(
	ctx context.Context,
	clusterID, buildID uuid.UUID,
) (string, error) {
	if !s.config.RegistryTokenMode {
		return "", nil
	}
	secret, hash := newRegistrySecret()
	created, err := s.queries.CreateBuildRegistryCredential(ctx, genDb.CreateBuildRegistryCredentialParams{
		SecretHash: hash,
		BuildID:    buildID,
		ClusterID:  clusterID,
	})
	if err != nil {
		return "", fmt.Errorf("store registry credential: %w", err)
	}
	if created == 0 {
		return "", errBuildLeftQueue
	}
	return secret, nil
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
		return fmt.Errorf("load builds held by the cluster: %w", err)
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

	if err := ss.server.failClusterBuilds(ctx, genDb.FailClusterBuildsParams{
		ClusterID:      &ss.clusterID,
		PreviousStatus: genDb.BuildStatusRunning,
		Held:           held,
		Message:        buildMissingMessage,
	}); err != nil {
		return err
	}
	return ss.reconcileBuilds(ctx)
}

func (s *AgentServer) failClusterBuilds(ctx context.Context, params genDb.FailClusterBuildsParams) error {
	var failed []genDb.FailClusterBuildsRow
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		rows, err := qtx.FailClusterBuilds(ctx, params)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := events.RecordBuildFailure(ctx, qtx, row.ID, row.ResourceID, params.Message); err != nil {
				return err
			}
		}
		failed = rows
		return nil
	})
	if err != nil {
		return fmt.Errorf("fail %s builds on cluster %s: %w", params.PreviousStatus, params.ClusterID, err)
	}

	sources := make([]buildSource, 0, len(failed))
	for _, row := range failed {
		slog.WarnContext(ctx, "build marked failed",
			"cluster_id", params.ClusterID,
			"build_id", row.ID,
			"reason", params.Message,
		)
		sources = append(sources, buildSource{id: row.ID, key: row.SourceKey})
	}
	deleteBuildSources(ctx, s.queries, s.sources, sources...)
	return nil
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
	switch status.GetPhase() {
	case agentv1.BuildPhase_BUILD_PHASE_PENDING:
		err = s.recordBuildPending(ctx, clusterID, buildID, status)
	case agentv1.BuildPhase_BUILD_PHASE_RUNNING:
		err = s.recordBuildRunning(ctx, clusterID, buildID, status)
	case agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED,
		agentv1.BuildPhase_BUILD_PHASE_FAILED,
		agentv1.BuildPhase_BUILD_PHASE_CANCELED:
		err = s.recordFinishedBuild(ctx, clusterID, buildID, status)
	default:
		return nil
	}
	if err != nil {
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

func (s *AgentServer) recordFinishedBuild(
	ctx context.Context,
	clusterID, buildID uuid.UUID,
	status *agentv1.BuildStatus,
) error {
	params := genDb.FinishBuildParams{ID: buildID, ClusterID: &clusterID}
	// The stored diagnostic can contain private information.
	var publicFailure string
	switch status.GetPhase() {
	case agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED:
		digest := status.GetImageDigest()
		if digest == "" {
			params.Status = genDb.BuildStatusFailed
			params.Message = buildMissingDigestMessage
			publicFailure = buildMissingDigestMessage
		} else {
			params.Status = genDb.BuildStatusSucceeded
			params.Message = messageOr(status.GetMessage(), buildSucceededMessage)
			params.ImageDigest = &digest
			if cache := status.GetCacheDigest(); cache != "" {
				params.CacheDigest = &cache
			}
		}
	case agentv1.BuildPhase_BUILD_PHASE_FAILED:
		params.Status = genDb.BuildStatusFailed
		params.Message = messageOr(status.GetMessage(), buildFailedMessage)
		publicFailure = buildFailedMessage
	case agentv1.BuildPhase_BUILD_PHASE_CANCELED:
		params.Status = genDb.BuildStatusCanceled
		params.Message = messageOr(status.GetMessage(), buildCanceledMessage)
	}

	var finished *buildSource
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		row, err := qtx.FinishBuild(ctx, params)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		finished = &buildSource{id: row.ID, key: row.SourceKey}
		switch params.Status {
		case genDb.BuildStatusSucceeded:
			data := map[string]any{events.FieldImageDigest: *params.ImageDigest}
			return events.RecordBuildOutcome(ctx, qtx, row.ID, row.ResourceID, events.BuildSucceeded, data)
		case genDb.BuildStatusFailed:
			return events.RecordBuildFailure(ctx, qtx, row.ID, row.ResourceID, publicFailure)
		default:
			return nil
		}
	})
	if err != nil {
		return fmt.Errorf("finish build: %w", err)
	}
	if finished == nil {
		return nil
	}

	slog.InfoContext(ctx, "build finished",
		"cluster_id", clusterID,
		"build_id", buildID,
		"status", params.Status,
	)
	deleteBuildSources(ctx, s.queries, s.sources, *finished)
	return nil
}
