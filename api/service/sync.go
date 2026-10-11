package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const applicationPhaseFailed = "Failed"

var errSyncSuperseded = errors.New("a newer sync stream for this cluster is connected")

type syncStream = connect.BidiStream[agentv1.SyncRequest, agentv1.SyncResponse]

func (s *AgentServer) Sync(ctx context.Context, stream *syncStream) error {
	cluster, err := s.authenticateAgent(ctx, stream.RequestHeader().Get("Authorization"))
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}

	listener := s.notifier.Listen(ctx, cluster.ID)
	defer listener.Close()

	generation, err := s.queries.BeginClusterSync(ctx, cluster.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin cluster sync", "cluster_id", cluster.ID, "error", err)
		return connect.NewError(connect.CodeUnavailable, ErrDB)
	}
	clusterIDText := cluster.ID.String()
	if notifyErr := s.queries.NotifyClusterPlacements(ctx, clusterIDText); notifyErr != nil {
		slog.WarnContext(ctx, "failed to announce sync stream", "cluster_id", cluster.ID, "error", notifyErr)
	}

	slog.InfoContext(ctx, "sync stream opened", "cluster_id", cluster.ID, "generation", generation)

	recvCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	messages := make(chan *agentv1.SyncRequest)
	recvErr := make(chan error, 1)
	go receiveSync(recvCtx, stream, messages, recvErr)

	session := newSyncSession(s, cluster.ID, stream.Send)

	dirty := true
	for {
		if dirty {
			if sendErr := session.sendPending(ctx); sendErr != nil {
				return sendErr
			}
			dirty = false
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-recvErr:
			slog.InfoContext(ctx, "sync stream closed", "cluster_id", cluster.ID, "error", err)
			return err
		case msg := <-messages:
			if handleErr := session.handle(ctx, msg); handleErr != nil {
				return handleErr
			}
		case <-listener.Wake():
			current, genErr := s.queries.GetClusterSyncGeneration(ctx, cluster.ID)
			if genErr != nil {
				slog.WarnContext(ctx, "failed to read sync generation", "cluster_id", cluster.ID, "error", genErr)
			}
			if genErr == nil && current != generation {
				slog.InfoContext(ctx, "sync stream superseded", "cluster_id", cluster.ID, "generation", generation)
				return connect.NewError(connect.CodeAborted, errSyncSuperseded)
			}
			dirty = true
		}
	}
}

func receiveSync(
	ctx context.Context,
	stream *syncStream,
	messages chan<- *agentv1.SyncRequest,
	recvErr chan<- error,
) {
	for {
		msg, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				recvErr <- nil
			} else {
				recvErr <- err
			}
			return
		}
		select {
		case messages <- msg:
		case <-ctx.Done():
			return
		}
	}
}

type syncSession struct {
	server    *AgentServer
	clusterID uuid.UUID
	sendFn    func(*agentv1.SyncResponse) error
	sent      map[uuid.UUID]int64
	builds    buildTracker
}

func newSyncSession(
	server *AgentServer,
	clusterID uuid.UUID,
	sendFn func(*agentv1.SyncResponse) error,
) *syncSession {
	return &syncSession{
		server:    server,
		clusterID: clusterID,
		sendFn:    sendFn,
		sent:      make(map[uuid.UUID]int64),
		builds:    newBuildTracker(),
	}
}

func (ss *syncSession) sendPending(ctx context.Context) error {
	if err := ss.sendPendingPlacements(ctx); err != nil {
		return err
	}
	return ss.reconcileBuilds(ctx)
}

func (ss *syncSession) sendPendingPlacements(ctx context.Context) error {
	pending, secrets, err := ss.server.readPlacements(ctx, func(
		ctx context.Context,
		q *genDb.Queries,
	) ([]genDb.Placement, error) {
		listed, listErr := q.ListPendingPlacements(ctx, ss.clusterID)
		if listErr != nil {
			return nil, listErr
		}
		unsent := make([]genDb.Placement, 0, len(listed))
		for _, placement := range listed {
			if ss.sent[placement.ID] != placement.DesiredRevision {
				unsent = append(unsent, placement)
			}
		}
		return unsent, nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to read pending placements", "cluster_id", ss.clusterID, "error", err)
		return nil
	}
	defer secrets.zero()
	for _, placement := range pending {
		if sendErr := ss.send(ctx, secrets, placement); sendErr != nil {
			return sendErr
		}
	}
	return nil
}

func (ss *syncSession) send(ctx context.Context, secrets *secretSnapshot, placement genDb.Placement) error {
	var envSecret *agentv1.EnvSecret
	if !placement.DesiredDeleted && len(placement.SecretNames) > 0 {
		secret, err := secrets.envSecret(ctx, placement)
		if isPlacementSecretError(err) {
			ss.server.recordApplyError(ctx, ss.clusterID, placement.ID, &agentv1.Applied{
				PlacementId: placement.ID.String(),
				Revision:    placement.DesiredRevision,
				Error:       err.Error(),
			})
			ss.sent[placement.ID] = placement.DesiredRevision
			return nil
		}
		if err != nil {
			slog.ErrorContext(ctx, "failed to load the secrets of a placement",
				"cluster_id", ss.clusterID,
				"placement_id", placement.ID,
				"revision", placement.DesiredRevision,
				"error", err,
			)
			return nil
		}
		envSecret = secret
		defer zeroEnvSecretData(envSecret.GetData())
	}
	msg := placementMessage(placement, envSecret)
	if err := ss.sendFn(msg); err != nil {
		return fmt.Errorf("send placement %s: %w", placement.ID, err)
	}
	ss.sent[placement.ID] = placement.DesiredRevision
	return nil
}

func placementMessage(placement genDb.Placement, envSecret *agentv1.EnvSecret) *agentv1.SyncResponse {
	resourceID := placement.ResourceID.String()
	placementID := placement.ID.String()
	if placement.DesiredDeleted {
		return &agentv1.SyncResponse{
			Message: &agentv1.SyncResponse_Delete{
				Delete: &agentv1.Delete{
					PlacementId: placementID,
					Revision:    placement.DesiredRevision,
					ResourceId:  resourceID,
				},
			},
		}
	}
	return &agentv1.SyncResponse{
		Message: &agentv1.SyncResponse_Apply{
			Apply: &agentv1.Apply{
				PlacementId: placementID,
				Revision:    placement.DesiredRevision,
				ResourceId:  resourceID,
				Application: placement.DesiredSpec,
				EnvSecret:   envSecret,
			},
		},
	}
}

func (ss *syncSession) handle(ctx context.Context, msg *agentv1.SyncRequest) error {
	switch m := msg.GetMessage().(type) {
	case *agentv1.SyncRequest_Inventory:
		return ss.reconcileInventory(ctx, m.Inventory)
	case *agentv1.SyncRequest_Applied:
		ss.server.recordApplied(ctx, ss.clusterID, m.Applied)
		return nil
	case *agentv1.SyncRequest_Status:
		ss.server.recordStatus(ctx, ss.clusterID, m.Status)
		return nil
	case *agentv1.SyncRequest_BuildStatus:
		return ss.handleBuildStatus(ctx, m.BuildStatus)
	default:
		slog.WarnContext(ctx, "ignoring empty sync message", "cluster_id", ss.clusterID)
		return nil
	}
}

type placementRevision struct {
	id               uuid.UUID
	desiredRevision  int64
	desiredDeleted   bool
	appliedRevision  int64
	observedRevision int64
	observed         bool
}

type inventoryAction int

const (
	inventoryInSync inventoryAction = iota
	inventorySend
	inventoryMarkApplied
	inventoryAhead
)

func diffInventory(rev placementRevision) inventoryAction {
	if rev.observed && rev.observedRevision > rev.desiredRevision {
		return inventoryAhead
	}
	if rev.desiredDeleted {
		return inventorySend
	}
	if !rev.observed || rev.observedRevision < rev.desiredRevision {
		return inventorySend
	}
	if rev.appliedRevision < rev.desiredRevision {
		return inventoryMarkApplied
	}
	return inventoryInSync
}

func (ss *syncSession) reconcileInventory(ctx context.Context, inventory *agentv1.Inventory) error {
	if err := ss.reconcilePlacementInventory(ctx, inventory); err != nil {
		return err
	}
	builds := inventory.GetBuilds()
	return ss.reconcileBuildInventory(ctx, builds)
}

func (ss *syncSession) reconcilePlacementInventory(ctx context.Context, inventory *agentv1.Inventory) error {
	observed := make(map[uuid.UUID]int64, len(inventory.GetEntries()))
	for _, entry := range inventory.GetEntries() {
		id, err := uuid.Parse(entry.GetPlacementId())
		if err != nil {
			slog.WarnContext(ctx, "ignoring inventory entry with invalid placement id",
				"cluster_id", ss.clusterID,
				"placement_id", entry.GetPlacementId(),
			)
			continue
		}
		observed[id] = entry.GetRevision()
	}

	rows, err := ss.server.queries.ListClusterPlacementRevisions(ctx, ss.clusterID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list placements for inventory", "cluster_id", ss.clusterID, "error", err)
		return nil
	}

	toSend := make([]uuid.UUID, 0)
	for _, row := range rows {
		observedRevision, present := observed[row.ID]
		action := diffInventory(placementRevision{
			id:               row.ID,
			desiredRevision:  row.DesiredRevision,
			desiredDeleted:   row.DesiredDeleted,
			appliedRevision:  row.AppliedRevision,
			observedRevision: observedRevision,
			observed:         present,
		})
		switch action {
		case inventorySend:
			toSend = append(toSend, row.ID)
		case inventoryMarkApplied:
			ss.server.markApplied(ctx, ss.clusterID, row.ID, row.DesiredRevision)
		case inventoryAhead:
			advanced, advanceErr := ss.server.advancePastRevision(
				ctx,
				ss.clusterID,
				row.ID,
				row.DesiredRevision,
				observedRevision,
			)
			if advanceErr != nil {
				slog.ErrorContext(ctx, "failed to advance placement past cluster revision",
					"cluster_id", ss.clusterID,
					"placement_id", row.ID,
					"error", advanceErr,
				)
				continue
			}
			if advanced {
				toSend = append(toSend, row.ID)
			}
		case inventoryInSync:
		}
	}

	slog.InfoContext(ctx, "inventory reconciled",
		"cluster_id", ss.clusterID,
		"entries", len(observed),
		"placements", len(rows),
		"to_send", len(toSend),
	)
	if len(toSend) == 0 {
		return nil
	}

	placements, secrets, err := ss.server.readPlacements(ctx, func(
		ctx context.Context,
		q *genDb.Queries,
	) ([]genDb.Placement, error) {
		return q.ListPlacementsByIDs(ctx, genDb.ListPlacementsByIDsParams{ClusterID: ss.clusterID, Ids: toSend})
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to load placements for inventory", "cluster_id", ss.clusterID, "error", err)
		return nil
	}
	defer secrets.zero()
	for _, placement := range placements {
		if sendErr := ss.send(ctx, secrets, placement); sendErr != nil {
			return sendErr
		}
	}
	return nil
}

func (s *AgentServer) advancePastRevision(
	ctx context.Context,
	clusterID, placementID uuid.UUID,
	expectedRevision, observedRevision int64,
) (bool, error) {
	revision, err := s.queries.AdvancePlacementPastRevision(ctx, genDb.AdvancePlacementPastRevisionParams{
		ObservedRevision: observedRevision,
		ID:               placementID,
		ClusterID:        clusterID,
		ExpectedRevision: expectedRevision,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("advance placement revision: %w", err)
	}
	slog.WarnContext(ctx, "cluster held a newer placement revision than desired; advanced past it",
		"cluster_id", clusterID,
		"placement_id", placementID,
		"observed_revision", observedRevision,
		"desired_revision", revision,
	)
	return true, nil
}

func (s *AgentServer) markApplied(ctx context.Context, clusterID, placementID uuid.UUID, revision int64) {
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		deploymentID, markErr := qtx.MarkPlacementApplied(ctx, genDb.MarkPlacementAppliedParams{
			ID:              placementID,
			ClusterID:       clusterID,
			DesiredRevision: revision,
		})
		if errors.Is(markErr, pgx.ErrNoRows) {
			return nil
		}
		if markErr != nil {
			return fmt.Errorf("mark placement applied: %w", markErr)
		}
		if deploymentID == nil {
			return nil
		}
		return qtx.AdvanceDeploymentStatus(ctx, genDb.AdvanceDeploymentStatusParams{
			Status:       genDb.DeploymentStatusDeploying,
			Message:      "Applied to the cluster",
			ID:           *deploymentID,
			FromStatuses: deploymentStatusNames(genDb.DeploymentStatusPending),
		})
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to record applied placement",
			"cluster_id", clusterID,
			"placement_id", placementID,
			"revision", revision,
			"error", err,
		)
	}
}

func (s *AgentServer) recordApplied(ctx context.Context, clusterID uuid.UUID, applied *agentv1.Applied) {
	placementID, err := uuid.Parse(applied.GetPlacementId())
	if err != nil {
		slog.WarnContext(ctx, "ignoring applied report with invalid placement id",
			"cluster_id", clusterID,
			"placement_id", applied.GetPlacementId(),
		)
		return
	}
	revision := applied.GetRevision()

	if applied.GetError() == "" {
		removed, deleteErr := s.queries.DeleteAppliedPlacement(ctx, genDb.DeleteAppliedPlacementParams{
			ID:              placementID,
			ClusterID:       clusterID,
			DesiredRevision: revision,
		})
		if deleteErr != nil {
			slog.ErrorContext(
				ctx,
				"failed to remove deleted placement",
				"placement_id",
				placementID,
				"error",
				deleteErr,
			)
			return
		}
		if removed > 0 {
			slog.InfoContext(
				ctx,
				"placement removed from cluster",
				"placement_id",
				placementID,
				"cluster_id",
				clusterID,
			)
			return
		}
		s.markApplied(ctx, clusterID, placementID, revision)
		return
	}

	s.recordApplyError(ctx, clusterID, placementID, applied)
}

func (s *AgentServer) recordApplyError(
	ctx context.Context,
	clusterID, placementID uuid.UUID,
	applied *agentv1.Applied,
) {
	revision := applied.GetRevision()
	errMessage := applied.GetError()
	slog.WarnContext(ctx, "placement failed to apply",
		"cluster_id", clusterID,
		"placement_id", placementID,
		"revision", revision,
		"retrying", applied.GetRetrying(),
		"error", errMessage,
	)

	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		row, setErr := qtx.SetPlacementApplyError(ctx, genDb.SetPlacementApplyErrorParams{
			ID:              placementID,
			ClusterID:       clusterID,
			DesiredRevision: revision,
			AppliedError:    &errMessage,
		})
		if errors.Is(setErr, pgx.ErrNoRows) {
			return nil
		}
		if setErr != nil {
			return fmt.Errorf("record apply error: %w", setErr)
		}
		if applied.GetRetrying() || row.DesiredDeleted || row.DeploymentID == nil {
			return nil
		}
		return qtx.AdvanceDeploymentStatus(ctx, genDb.AdvanceDeploymentStatusParams{
			Status:  genDb.DeploymentStatusFailed,
			Message: errMessage,
			ID:      *row.DeploymentID,
			FromStatuses: deploymentStatusNames(
				genDb.DeploymentStatusPending,
				genDb.DeploymentStatusDeploying,
				genDb.DeploymentStatusRunning,
			),
		})
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to record apply error", "placement_id", placementID, "error", err)
	}
}

func (s *AgentServer) recordStatus(ctx context.Context, clusterID uuid.UUID, status *agentv1.PlacementStatus) {
	placementID, err := uuid.Parse(status.GetPlacementId())
	if err != nil {
		slog.WarnContext(ctx, "ignoring status with invalid placement id",
			"cluster_id", clusterID,
			"placement_id", status.GetPlacementId(),
		)
		return
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		row, updateErr := qtx.UpdatePlacementStatus(ctx, genDb.UpdatePlacementStatusParams{
			ObservedRevision: status.GetObservedRevision(),
			Ready:            status.GetReady(),
			ReadyReplicas:    status.GetReadyReplicas(),
			StatusPhase:      status.GetPhase(),
			StatusMessage:    status.GetMessage(),
			ID:               placementID,
			ClusterID:        clusterID,
		})
		if errors.Is(updateErr, pgx.ErrNoRows) {
			return nil
		}
		if updateErr != nil {
			return fmt.Errorf("update placement status: %w", updateErr)
		}
		if row.DeploymentID == nil || status.GetObservedRevision() != row.DesiredRevision {
			return nil
		}
		transition := deploymentTransitionForStatus(status)
		return qtx.AdvanceDeploymentStatus(ctx, genDb.AdvanceDeploymentStatusParams{
			Status:       transition.status,
			Message:      status.GetMessage(),
			ID:           *row.DeploymentID,
			FromStatuses: deploymentStatusNames(transition.from...),
		})
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to record placement status", "placement_id", placementID, "error", err)
	}
}

type deploymentTransition struct {
	status genDb.DeploymentStatus
	from   []genDb.DeploymentStatus
}

func deploymentTransitionForStatus(status *agentv1.PlacementStatus) deploymentTransition {
	if status.GetPhase() == applicationPhaseFailed {
		return deploymentTransition{
			status: genDb.DeploymentStatusFailed,
			from: []genDb.DeploymentStatus{
				genDb.DeploymentStatusPending,
				genDb.DeploymentStatusDeploying,
				genDb.DeploymentStatusRunning,
			},
		}
	}
	if status.GetReady() {
		return deploymentTransition{
			status: genDb.DeploymentStatusRunning,
			from: []genDb.DeploymentStatus{
				genDb.DeploymentStatusPending,
				genDb.DeploymentStatusDeploying,
				genDb.DeploymentStatusFailed,
			},
		}
	}
	return deploymentTransition{
		status: genDb.DeploymentStatusDeploying,
		from:   []genDb.DeploymentStatus{genDb.DeploymentStatusPending},
	}
}

func deploymentStatusNames(statuses ...genDb.DeploymentStatus) []string {
	names := make([]string, 0, len(statuses))
	for _, status := range statuses {
		names = append(names, string(status))
	}
	return names
}
