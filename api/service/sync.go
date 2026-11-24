package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/tracing"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	"github.com/team-loco/loco/internal/rpcerr"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var errSyncSuperseded = errors.New("a newer sync stream for this cluster is connected")

// syncReportQueue is how many agent reports one sync stream queues; a full queue pauses reading the stream.
const syncReportQueue = 1024

type syncStream = agentv1connect.AgentServiceSyncServerStream

func (s *AgentServer) Sync(ctx context.Context, stream syncStream) error {
	info, _ := connect.CallInfoForServerContext(ctx)
	cluster, err := s.clusters.Authenticate(ctx, info.RequestHeader().Get("Authorization"))
	if err != nil {
		return agentAuthError(err)
	}

	listener := s.notifier.Listen(cluster.ID)
	defer listener.Close()

	generation, err := s.queries.BeginClusterSync(ctx, cluster.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return rpcerr.New(connect.CodePermissionDenied, ErrClusterRemoved)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin cluster sync", "cluster_id", cluster.ID, "error", err)
		return connect.NewError(connect.CodeUnavailable, publicRequestFailed).WithCause(err)
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

	send := func(msg *agentv1.SyncResponse) error {
		sendCtx, span := s.responseSpan(ctx, cluster.ID, msg)
		err := s.checkSyncGeneration(sendCtx, cluster.ID, generation)
		if err == nil {
			err = stream.Send(msg)
		}
		tracing.End(span, err)
		return err
	}
	session := newSyncSession(s, cluster.ID, send)
	session.generation = generation
	// A stream that ends drops its queued reports; the agent replays every placement's status on its next stream.
	reportCtx, dropReports := context.WithCancel(ctx)
	var reports sync.WaitGroup
	defer reports.Wait()
	defer dropReports()
	reports.Go(func() { session.applyReports(reportCtx) })

	poll := time.NewTicker(s.notifier.PollInterval())
	defer poll.Stop()
	dirty := true
	var retryDue <-chan time.Time
	for {
		if err := s.checkSyncGeneration(ctx, cluster.ID, generation); err != nil {
			return err
		}
		if dirty {
			if sendErr := session.sendPending(ctx); sendErr != nil {
				return sendErr
			}
			dirty = false
			session.retryRecorded = true
		}
		if session.retryRecorded {
			session.retryRecorded = false
			retryDue = nil
			if wait, scheduled := session.nextRetry(ctx); scheduled {
				retryDue = time.After(wait)
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-recvErr:
			slog.InfoContext(ctx, "sync stream closed", "cluster_id", cluster.ID, "error", err)
			return err
		case msg := <-messages:
			if err := s.checkSyncGeneration(ctx, cluster.ID, generation); err != nil {
				return err
			}
			if handleErr := session.handle(ctx, msg); handleErr != nil {
				return handleErr
			}
		case <-listener.Wake():
			dirty = true
		case <-poll.C:
			dirty = true
		case <-retryDue:
			dirty = true
		case <-session.settledReady:
			if err := session.settle(ctx); err != nil {
				return err
			}
		}
	}
}

func (s *AgentServer) checkSyncGeneration(ctx context.Context, clusterID uuid.UUID, generation int64) error {
	current, err := s.queries.GetClusterSyncGeneration(ctx, clusterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return rpcerr.New(connect.CodePermissionDenied, ErrClusterRemoved)
	}
	if err != nil {
		return connect.NewError(connect.CodeUnavailable, publicRequestFailed).WithCause(err)
	}
	if current != generation {
		return rpcerr.New(connect.CodeAborted, errSyncSuperseded)
	}
	return nil
}

func receiveSync(
	ctx context.Context,
	stream syncStream,
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
	generation    int64
	server        *AgentServer
	clusterID     uuid.UUID
	sendFn        func(*agentv1.SyncResponse) error
	sent          map[uuid.UUID]int64
	builds        buildTracker
	retryRecorded bool
	// reports queues the agent reports that write the database, and so can wait for a write's locks,
	// for one worker to apply in order.
	reports chan func(context.Context)
	// settled holds what applied reports change in the session, for the receive loop to run in order.
	settledMu    sync.Mutex
	settled      []settleFunc
	settledReady chan struct{}
}

// settleFunc runs on the receive loop, which owns the session's state and the stream's sends.
type settleFunc func(context.Context) error

func settleErr(err error) settleFunc {
	return func(context.Context) error { return err }
}

func newSyncSession(
	server *AgentServer,
	clusterID uuid.UUID,
	sendFn func(*agentv1.SyncResponse) error,
) *syncSession {
	return &syncSession{
		server:       server,
		clusterID:    clusterID,
		sendFn:       sendFn,
		sent:         make(map[uuid.UUID]int64),
		builds:       newBuildTracker(),
		reports:      make(chan func(context.Context), syncReportQueue),
		settledReady: make(chan struct{}, 1),
	}
}

// applyReports applies queued reports one at a time until ctx ends.
func (ss *syncSession) applyReports(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case apply := <-ss.reports:
			apply(ctx)
		}
	}
}

// queueReport waits for room in the report queue, so a full queue stops reading the stream. The
// settleFunc the report returns, if any, runs on the receive loop.
func (ss *syncSession) queueReport(ctx context.Context, apply func(context.Context) settleFunc) error {
	select {
	case ss.reports <- func(ctx context.Context) { ss.addSettled(apply(ctx)) }:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (ss *syncSession) addSettled(settle settleFunc) {
	if settle == nil {
		return
	}
	ss.settledMu.Lock()
	ss.settled = append(ss.settled, settle)
	ss.settledMu.Unlock()
	select {
	case ss.settledReady <- struct{}{}:
	default:
	}
}

// settle runs what the applied reports change in the session, in the order they were applied.
func (ss *syncSession) settle(ctx context.Context) error {
	ss.settledMu.Lock()
	settled := ss.settled
	ss.settled = nil
	ss.settledMu.Unlock()
	for _, settle := range settled {
		if err := settle(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (ss *syncSession) nextRetry(ctx context.Context) (time.Duration, bool) {
	waitMs, err := ss.server.queries.NextPlacementRetry(ctx, ss.clusterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to read the next placement retry", "cluster_id", ss.clusterID, "error", err)
		return 0, false
	}
	return time.Duration(waitMs) * time.Millisecond, true
}

func (ss *syncSession) sendPending(ctx context.Context) error {
	if err := ss.sendPendingPlacements(ctx); err != nil {
		return err
	}
	return ss.reconcileBuilds(ctx)
}

func (ss *syncSession) sendPendingPlacements(ctx context.Context) error {
	listed, err := ss.server.queries.ListPendingPlacements(ctx, ss.clusterID)
	if err != nil {
		return fmt.Errorf("list pending placements: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(listed))
	for _, placement := range listed {
		if ss.sent[placement.ID] != placement.DesiredRevision {
			ids = append(ids, placement.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return ss.sendPlacements(ctx, ids)
}

func (ss *syncSession) sendPlacements(ctx context.Context, ids []uuid.UUID) error {
	placements, secrets, err := ss.server.readPlacements(ctx, func(
		ctx context.Context,
		q *genDb.Queries,
	) ([]syncPlacement, error) {
		return listSyncPlacements(ctx, q, ss.clusterID, ids)
	})
	if err != nil {
		return fmt.Errorf("read placements: %w", err)
	}
	defer secrets.zero()
	for _, placement := range placements {
		if sendErr := ss.send(ctx, secrets, placement); sendErr != nil {
			return sendErr
		}
	}
	return nil
}

func listSyncPlacements(
	ctx context.Context,
	q *genDb.Queries,
	clusterID uuid.UUID,
	ids []uuid.UUID,
) ([]syncPlacement, error) {
	rows, err := q.ListPlacementsByIDs(ctx, genDb.ListPlacementsByIDsParams{ClusterID: clusterID, Ids: ids})
	if err != nil {
		return nil, err
	}
	placements := make([]syncPlacement, 0, len(rows))
	for _, row := range rows {
		placement := row.Placement
		if row.Suspended {
			placement.DesiredDeleted = true
			placement.DesiredSpec = nil
		}
		placements = append(placements, syncPlacement{Placement: placement, traceparent: row.Traceparent})
	}
	return placements, nil
}

func (ss *syncSession) send(ctx context.Context, secrets *secretSnapshot, listed syncPlacement) error {
	placement := listed.Placement
	var envSecret *agentv1.EnvSecret
	if !placement.DesiredDeleted && len(placement.SecretEnv) > 0 {
		secret, err := secrets.envSecret(ctx, placement)
		if isPlacementSecretError(err) {
			return ss.reject(ctx, placement, err)
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
	if placement.RestartedAt != nil && !placement.DesiredDeleted {
		spec, err := restartedSpec(placement)
		if err != nil {
			return ss.reject(ctx, placement, err)
		}
		placement.DesiredSpec = spec
	}
	msg := placementMessage(placement, envSecret, listed.traceparent)
	if err := ss.sendFn(msg); err != nil {
		return fmt.Errorf("send placement %s: %w", placement.ID, err)
	}
	ss.sent[placement.ID] = placement.DesiredRevision
	return nil
}

// reject queues the error as the placement's apply error. The placement counts as sent until the
// error is recorded, then is left unsent for the next sync to retry.
func (ss *syncSession) reject(ctx context.Context, placement genDb.Placement, err error) error {
	ss.sent[placement.ID] = placement.DesiredRevision
	applied := &agentv1.Applied{
		PlacementId: placement.ID.String(),
		Revision:    placement.DesiredRevision,
		Error:       err.Error(),
	}
	return ss.queueReport(ctx, func(ctx context.Context) settleFunc {
		ss.server.recordApplyError(ctx, ss.clusterID, placement.ID, applied)
		return ss.retryUnsent(placement.ID)
	})
}

// retryUnsent leaves the placement unsent and reschedules the next retry.
func (ss *syncSession) retryUnsent(placementID uuid.UUID) settleFunc {
	return func(context.Context) error {
		delete(ss.sent, placementID)
		ss.retryRecorded = true
		return nil
	}
}

func placementMessage(
	placement genDb.Placement,
	envSecret *agentv1.EnvSecret,
	traceparent string,
) *agentv1.SyncResponse {
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
				Traceparent: traceparent,
			},
		},
	}
}

// handle records a heartbeat at once and queues every other report.
func (ss *syncSession) handle(ctx context.Context, msg *agentv1.SyncRequest) error {
	if heartbeat := msg.GetHeartbeat(); heartbeat != nil {
		ss.server.recordHeartbeat(ctx, ss.clusterID, heartbeat)
		return nil
	}
	return ss.queueReport(ctx, func(ctx context.Context) settleFunc {
		ctx, span := ss.server.requestSpan(ctx, ss.clusterID, msg)
		defer tracing.End(span, nil)
		return ss.apply(ctx, msg)
	})
}

// apply records a report and returns what it changes in the session.
func (ss *syncSession) apply(ctx context.Context, msg *agentv1.SyncRequest) settleFunc {
	switch m := msg.GetMessage().(type) {
	case *agentv1.SyncRequest_Status:
		ss.server.recordStatus(ctx, ss.clusterID, ss.generation, m.Status)
		return nil
	case *agentv1.SyncRequest_Inventory:
		return ss.applyInventory(ctx, m.Inventory)
	case *agentv1.SyncRequest_Applied:
		ss.server.recordApplied(ctx, ss.clusterID, m.Applied)
		placementID, err := uuid.Parse(m.Applied.GetPlacementId())
		if err != nil || m.Applied.GetError() == "" || m.Applied.GetRetrying() {
			return nil
		}
		return ss.retryUnsent(placementID)
	case *agentv1.SyncRequest_BuildStatus:
		return ss.applyBuildStatus(ctx, m.BuildStatus)
	default:
		slog.WarnContext(ctx, "ignoring empty sync message", "cluster_id", ss.clusterID)
		return nil
	}
}

type placementRevision struct {
	id               uuid.UUID
	desiredRevision  int64
	desiredDeleted   bool
	suspended        bool
	appliedRevision  int64
	observedRevision int64
	observed         bool
	envSecretPending bool
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
	if rev.suspended && !rev.observed && rev.appliedRevision >= rev.desiredRevision {
		return inventoryInSync
	}
	if rev.suspended {
		return inventorySend
	}
	if !rev.observed || rev.observedRevision < rev.desiredRevision {
		return inventorySend
	}
	if rev.envSecretPending {
		return inventorySend
	}
	if rev.appliedRevision < rev.desiredRevision {
		return inventoryMarkApplied
	}
	return inventoryInSync
}

// applyInventory records what the inventory shows the cluster holds; its settleFunc resends
// what the cluster lacks and reconciles the cluster's workspaces and builds.
func (ss *syncSession) applyInventory(ctx context.Context, inventory *agentv1.Inventory) settleFunc {
	toSend, err := ss.recordPlacementInventory(ctx, inventory)
	if err != nil {
		return settleErr(err)
	}
	builds := readBuildInventory(ctx, ss.clusterID, inventory.GetBuilds())
	if err := ss.server.failClusterBuilds(ctx, genDb.FailClusterBuildsParams{
		ClusterID:      &ss.clusterID,
		PreviousStatus: genDb.BuildStatusRunning,
		Held:           builds.held,
		Message:        buildMissingMessage,
	}); err != nil {
		return settleErr(err)
	}
	return func(ctx context.Context) error {
		if len(toSend) > 0 {
			if err := ss.sendPlacements(ctx, toSend); err != nil {
				return err
			}
		}
		if err := ss.reconcileWorkspaceInventory(ctx, inventory.GetWorkspaceIds()); err != nil {
			return err
		}
		ss.builds.track(builds)
		return ss.reconcileBuilds(ctx)
	}
}

// recordPlacementInventory records the placements the inventory shows applied and returns those to send.
func (ss *syncSession) recordPlacementInventory(
	ctx context.Context,
	inventory *agentv1.Inventory,
) ([]uuid.UUID, error) {
	observed := make(map[uuid.UUID]*agentv1.InventoryEntry, len(inventory.GetEntries()))
	for _, entry := range inventory.GetEntries() {
		id, err := uuid.Parse(entry.GetPlacementId())
		if err != nil {
			slog.WarnContext(ctx, "ignoring inventory entry with invalid placement id",
				"cluster_id", ss.clusterID,
				"placement_id", entry.GetPlacementId(),
			)
			continue
		}
		observed[id] = entry
	}

	rows, err := ss.server.queries.ListClusterPlacementRevisions(ctx, ss.clusterID)
	if err != nil {
		return nil, fmt.Errorf("list placements for inventory: %w", err)
	}

	toSend := make([]uuid.UUID, 0)
	for _, row := range rows {
		entry, present := observed[row.ID]
		observedRevision := entry.GetRevision()
		action := diffInventory(placementRevision{
			id:               row.ID,
			desiredRevision:  row.DesiredRevision,
			desiredDeleted:   row.DesiredDeleted,
			suspended:        row.Suspended,
			appliedRevision:  row.AppliedRevision,
			observedRevision: observedRevision,
			observed:         present,
			envSecretPending: entry.GetEnvSecretPending(),
		})
		switch action {
		case inventorySend:
			if !row.SendHeld {
				toSend = append(toSend, row.ID)
			}
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
	return toSend, nil
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
	err := s.queries.MarkPlacementApplied(ctx, genDb.MarkPlacementAppliedParams{
		ID:              placementID,
		ClusterID:       clusterID,
		DesiredRevision: revision,
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
		if _, resourceErr := qtx.LockPlacementResource(
			ctx,
			genDb.LockPlacementResourceParams{ID: placementID, ClusterID: clusterID},
		); errors.Is(
			resourceErr,
			pgx.ErrNoRows,
		) {
			return nil
		} else if resourceErr != nil {
			return fmt.Errorf("lock apply resource: %w", resourceErr)
		}

		row, setErr := qtx.SetPlacementApplyError(ctx, genDb.SetPlacementApplyErrorParams{
			ID:                placementID,
			ClusterID:         clusterID,
			DesiredRevision:   revision,
			AppliedError:      &errMessage,
			Retrying:          applied.GetRetrying(),
			MaxAttempts:       s.config.ApplyAttempts,
			RetryDelaySeconds: s.config.ApplyRetryDelay,
		})
		if errors.Is(setErr, pgx.ErrNoRows) {
			return nil
		}
		if setErr != nil {
			return fmt.Errorf("record apply error: %w", setErr)
		}
		if !row.ApplyFailed || row.DesiredDeleted || row.Suspended || row.DeploymentID == nil {
			return nil
		}
		live := placementStatusNames(placementLiveStatuses...)
		cause := transitionCause{message: errMessage}
		return advancePlacementStatus(ctx, qtx, *row.DeploymentID, clusterID, genDb.PlacementStatusFailed,
			live, cause)
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to record apply error", "placement_id", placementID, "error", err)
	}
}

func (s *AgentServer) recordStatus(
	ctx context.Context,
	clusterID uuid.UUID,
	generation int64,
	status *agentv1.PlacementStatus,
) {
	placementID, err := uuid.Parse(status.GetPlacementId())
	if err != nil {
		slog.WarnContext(ctx, "ignoring status with invalid placement id",
			"cluster_id", clusterID,
			"placement_id", status.GetPlacementId(),
		)
		return
	}

	if !validRolloutReport(ctx, status) {
		return
	}
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		placement := genDb.LockPlacementResourceParams{ID: placementID, ClusterID: clusterID}
		if _, resourceErr := qtx.LockPlacementResource(ctx, placement); errors.Is(resourceErr, pgx.ErrNoRows) {
			return nil
		} else if resourceErr != nil {
			return fmt.Errorf("lock status resource: %w", resourceErr)
		}
		current, lockErr := qtx.LockClusterSyncGeneration(ctx, clusterID)
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return nil
		}
		if lockErr != nil {
			return fmt.Errorf("lock sync generation: %w", lockErr)
		}
		if current != generation {
			return nil
		}
		row, updateErr := qtx.UpdatePlacementStatus(ctx, genDb.UpdatePlacementStatusParams{
			ObservedRevision: status.GetObservedRevision(),
			ReadyReplicas:    status.GetReadyReplicas(),
			ID:               placementID,
			ClusterID:        clusterID,
		})
		if errors.Is(updateErr, pgx.ErrNoRows) {
			return nil
		}
		if updateErr != nil {
			return fmt.Errorf("update placement status: %w", updateErr)
		}
		if reportErr := s.applyPlacementReport(ctx, qtx, clusterID, placementID, status, row); reportErr != nil {
			return reportErr
		}
		return recordRegionStatuses(
			ctx,
			qtx,
			row.ResourceID,
			ClusterSilentAfter(s.heartbeatInterval),
		)
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to record placement status", "placement_id", placementID, "error", err)
	}
}

// applyPlacementReport records the rollout a placement's report describes and advances the
// placement's deployment.
func (*AgentServer) applyPlacementReport(
	ctx context.Context,
	qtx *genDb.Queries,
	clusterID, placementID uuid.UUID,
	status *agentv1.PlacementStatus,
	row genDb.UpdatePlacementStatusRow,
) error {
	reason := status.GetProblem().GetReason()
	// A revision without a problem has not recovered until it serves.
	if status.GetObservedRevision() == row.DesiredRevision && (reason != "" || status.GetAvailable()) {
		changed, changeErr := qtx.ChangePlacementProblemReason(ctx, genDb.ChangePlacementProblemReasonParams{
			ID: placementID, Reason: reason,
		})
		if changeErr != nil {
			return fmt.Errorf("change placement problem: %w", changeErr)
		}
		if changed > 0 {
			message, _ := locoControllerV1.ProblemMessage(locoControllerV1.ProblemReason(reason))
			kind := events.ResourceDegraded
			if reason == "" {
				kind = events.ResourceRecovered
			}
			if recordErr := events.Record(ctx, qtx, events.Event{
				Type:          kind,
				ActorType:     events.ActorSystem,
				ResourceID:    &row.ResourceID,
				EnvironmentID: &row.EnvironmentID,
				DeploymentID:  row.DeploymentID,
				SubjectType:   events.SubjectResource,
				SubjectID:     &row.ResourceID,
				Data: map[string]any{
					events.FieldReason:      reason,
					events.FieldMessage:     message,
					events.FieldPlacementID: placementID.String(),
					events.FieldRegion:      row.Region,
				},
			}); recordErr != nil {
				return recordErr
			}
		}
	}

	// Older agents report only current-revision rollout context.
	if status.GetDeploymentId() == "" &&
		(row.DeploymentID == nil || status.GetObservedRevision() != row.DesiredRevision) {
		return nil
	}
	deployment, captureErr := capturePlacementRollout(ctx, qtx, clusterID, row, status)
	if captureErr != nil {
		return captureErr
	}
	if row.DeploymentID == nil || deployment.ID != *row.DeploymentID ||
		status.GetObservedRevision() != row.DesiredRevision {
		return nil
	}
	placement, getErr := qtx.GetDeploymentPlacement(ctx, genDb.GetDeploymentPlacementParams{
		DeploymentID: deployment.ID, ClusterID: clusterID,
	})
	if getErr != nil {
		return fmt.Errorf("read deployment placement: %w", getErr)
	}
	next := nextPlacementStatus(placement.Status, status)
	if next != placement.Status {
		if transitionErr := advancePlacementStatus(
			ctx, qtx, deployment.ID, clusterID, next, placementStatusNames(placement.Status),
			reportCause(status),
		); transitionErr != nil {
			return transitionErr
		}
	}
	if next != genDb.PlacementStatusRunning || !status.GetDeployed() {
		return nil
	}
	deployed, deployErr := qtx.DeployDeploymentPlacement(ctx, genDb.DeployDeploymentPlacementParams{
		DeploymentID: deployment.ID, ClusterID: clusterID,
	})
	if deployErr != nil {
		return fmt.Errorf("record placement deployed: %w", deployErr)
	}
	if deployed > 0 {
		if recordErr := recordDeploymentStatus(ctx, qtx, deployment.ID, reportCause(status)); recordErr != nil {
			return recordErr
		}
	}
	return supersedePlacements(ctx, qtx, deployment.ID, clusterID, true)
}

// transitionCause is why a placement moved: the message events carry and, when the controller
// stopped the revision, the stop.
type transitionCause struct {
	message string
	stopped *agentv1.StoppedRevision
}

func reportCause(report *agentv1.PlacementStatus) transitionCause {
	return transitionCause{
		message: problemReportMessage(report.GetProblem()),
		stopped: report.GetStopped(),
	}
}

// advancePlacementStatus moves one region of a deployment to status and records the
// deployment's aggregate transition.
func advancePlacementStatus(
	ctx context.Context,
	qtx *genDb.Queries,
	deploymentID, clusterID uuid.UUID,
	status genDb.PlacementStatus,
	from []string,
	cause transitionCause,
) error {
	changed, err := qtx.SetDeploymentPlacementStatus(ctx, genDb.SetDeploymentPlacementStatusParams{
		Status: status, DeploymentID: deploymentID, ClusterID: clusterID, FromStatuses: from,
	})
	if err != nil {
		return fmt.Errorf("advance placement status: %w", err)
	}
	if changed == 0 {
		return nil
	}
	return recordDeploymentStatus(ctx, qtx, deploymentID, cause)
}

// deploymentStatusEvent is the failure event a deployment's aggregate transition records, if any.
func deploymentStatusEvent(row genDb.RecomputeDeploymentStatusRow) (string, bool) {
	if row.Status == row.PreviousStatus {
		return "", false
	}
	switch row.Status {
	case genDb.DeploymentStatusFailed:
		return events.DeploymentFailed, true
	case genDb.DeploymentStatusCrashed:
		return events.DeploymentCrashed, true
	default:
		return "", false
	}
}

// recordDeploymentStatus recomputes a deployment's aggregate status from its placements and
// records deployment.failed or deployment.crashed on that transition.
func recordDeploymentStatus(
	ctx context.Context,
	qtx *genDb.Queries,
	id uuid.UUID,
	cause transitionCause,
) error {
	status, err := recomputeDeploymentStatus(ctx, qtx, id)
	if err != nil {
		return fmt.Errorf("recompute deployment status: %w", err)
	}
	kind, recorded := deploymentStatusEvent(status)
	if !recorded {
		return nil
	}
	return recordDeploymentEvent(ctx, qtx, id, kind, status, cause)
}

// recordDeploymentEvent records a deployment event of kind with the regions its status names.
func recordDeploymentEvent(
	ctx context.Context,
	qtx *genDb.Queries,
	id uuid.UUID,
	kind string,
	status genDb.RecomputeDeploymentStatusRow,
	cause transitionCause,
) error {
	d, err := qtx.GetDeploymentEvent(ctx, id)
	if err != nil {
		return fmt.Errorf("get deployment event context: %w", err)
	}
	spec, err := converter.DeserializeDeploymentSpec(d.Spec, d.ResourceType)
	if err != nil {
		return fmt.Errorf("decode spec of deployment %s: %w", id, err)
	}
	placements, err := qtx.ListDeploymentPlacements(ctx, id)
	if err != nil {
		return fmt.Errorf("list deployment placements: %w", err)
	}
	failed := status.Status == genDb.DeploymentStatusFailed
	crashed := status.Status == genDb.DeploymentStatusCrashed
	regions := []string{}
	for _, placement := range placements {
		if placement.Status == genDb.PlacementStatusCanceled ||
			failed && placement.Status != genDb.PlacementStatusFailed ||
			crashed && placement.Status != genDb.PlacementStatusCrashed {
			continue
		}
		regions = append(regions, placement.Region)
	}
	data := map[string]any{
		events.FieldDeploymentID:  id.String(),
		events.FieldResourceID:    d.ResourceID.String(),
		events.FieldEnvironmentID: d.EnvironmentID.String(),
		events.FieldRegions:       regions,
		events.FieldMessage:       cause.message,
		events.FieldResourceName:  d.ResourceName,
	}
	if restarts := cause.stopped.GetRestarts(); restarts > 0 {
		data[events.FieldRestarts] = restarts
	}
	if buildID := spec.GetService().GetBuild().GetBuildId(); buildID != "" {
		data[events.FieldBuildID] = buildID
	}
	events.AddCreator(data, d.CreatorID, d.CreatorEmail, d.CreatorName)
	if err := events.Record(ctx, qtx, events.Event{
		Type:          kind,
		ActorType:     string(d.ActorType),
		ActorID:       &d.ActorID,
		ResourceID:    &d.ResourceID,
		EnvironmentID: &d.EnvironmentID,
		SubjectType:   events.SubjectDeployment,
		SubjectID:     &id,
		Data:          data,
	}); err != nil {
		return err
	}
	return nil
}
