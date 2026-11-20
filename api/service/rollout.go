package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	activityInfo         = "info"
	activityOK           = "ok"
	activityWarn         = "warn"
	activityError        = "error"
	rolloutActivityLimit = 500
	activityLimitMessage = "Activity limit reached"
	exitSIGTERM          = 143
)

type rolloutSnapshot struct {
	PlacementID     string                  `json:"placementId,omitempty"`
	Replicas        []*deploymentv1.Replica `json:"replicas,omitempty"`
	Problem         *agentv1.ProblemReport  `json:"problem,omitempty"`
	DesiredReplicas int32                   `json:"desiredReplicas,omitempty"`
	Count           int32                   `json:"count,omitempty"`
	// Started holds each replica's restart count when its last Starting entry was written.
	Started map[string]int32 `json:"started,omitempty"`
}

type activityEntry struct {
	Message     string
	Severity    string
	ReplicaUID  string
	ReplicaName string
	At          time.Time
}

func diffRollout(previous rolloutSnapshot, report *agentv1.PlacementStatus) []activityEntry {
	old := make(map[string]*deploymentv1.Replica, len(previous.Replicas))
	hadPrevious := false
	for _, replica := range previous.Replicas {
		old[replica.GetUid()] = replica
		if replica.GetPlacementRevision() < report.GetObservedRevision() && !terminalReplica(replica) {
			hadPrevious = true
		}
	}
	current := make(map[string]bool, len(report.GetReplicas()))
	livePrevious := false
	var entries []activityEntry
	for _, replica := range report.GetReplicas() {
		uid := replica.GetUid()
		current[uid] = true
		if replica.GetPlacementRevision() < report.GetObservedRevision() && !terminalReplica(replica) {
			livePrevious = true
		}
		entries = append(entries, restartActivity(old[uid], replica, report)...)
		if before := old[uid]; before != nil && before.GetState() == replica.GetState() {
			continue
		}
		if restarts, ok := previous.Started[uid]; ok && replica.GetState() == string(loco.ReplicaStarting) &&
			restarts == replica.GetRestarts() {
			continue
		}
		message, severity := replicaActivityMessage(replica)
		entry := activityEntry{
			Message:     message,
			Severity:    severity,
			ReplicaUID:  uid,
			ReplicaName: replica.GetName(),
		}
		if replica.GetStateSince() != nil {
			entry.At = replica.GetStateSince().AsTime()
		}
		entries = append(entries, entry)
	}
	for _, replica := range previous.Replicas {
		if !current[replica.GetUid()] && !terminalReplica(replica) {
			entries = append(
				entries,
				activityEntry{
					Message:     "Stopped · no exit code seen",
					Severity:    activityInfo,
					ReplicaUID:  replica.GetUid(),
					ReplicaName: replica.GetName(),
				},
			)
		}
	}
	if previous.DesiredReplicas != report.GetDesiredReplicas() {
		entries = append(
			entries,
			activityEntry{
				Message:  fmt.Sprintf("Scaled to %d replicas", report.GetDesiredReplicas()),
				Severity: activityInfo,
			},
		)
	}
	if previous.Problem.GetReason() != report.GetProblem().GetReason() {
		message := "Problem cleared"
		severity := activityOK
		if problem := report.GetProblem(); problem != nil {
			message, _ = loco.ProblemMessage(loco.ProblemReason(problem.GetReason()))
			severity = activityError
			if problem.GetDetail() != "" {
				message += " · " + problem.GetDetail()
			}
		}
		entries = append(entries, activityEntry{Message: message, Severity: severity})
	}
	if hadPrevious && !livePrevious {
		entries = append(entries, activityEntry{Message: "Previous version gone", Severity: activityOK})
	}
	count := previous.Count
	var limited []activityEntry
	for _, entry := range entries {
		if count > rolloutActivityLimit {
			continue
		}
		limited = append(limited, entry)
		count++
		if count == rolloutActivityLimit {
			limited = append(limited, activityEntry{Message: activityLimitMessage, Severity: activityWarn})
			count++
		}
	}
	return limited
}

// startedReplicas records the restart count of each reported replica that is starting, and keeps
// the earlier record of the others.
func startedReplicas(previous map[string]int32, report *agentv1.PlacementStatus) map[string]int32 {
	started := make(map[string]int32)
	for _, replica := range report.GetReplicas() {
		uid := replica.GetUid()
		if replica.GetState() == string(loco.ReplicaStarting) {
			started[uid] = replica.GetRestarts()
		} else if restarts, ok := previous[uid]; ok {
			started[uid] = restarts
		}
	}
	return started
}

// restartActivity is one entry per restart the replica reports that its previous report did not.
func restartActivity(before, replica *deploymentv1.Replica, report *agentv1.PlacementStatus) []activityEntry {
	var seen time.Time
	if restarts := before.GetRestartsAt(); len(restarts) > 0 {
		seen = restarts[len(restarts)-1].AsTime()
	}
	last := replica.GetLastRestart()
	restart := loco.ReplicaRestart{Cause: loco.RestartCause(last.GetCause()), ExitCode: last.GetExitCode()}
	var entries []activityEntry
	for i, at := range replica.GetRestartsAt() {
		if !at.AsTime().After(seen) {
			continue
		}
		message := fmt.Sprintf("Restarted after %s · %d", restart.After(), i+1)
		if budget := report.GetFailurePolicy().GetMaxRestarts(); budget > 0 {
			message += fmt.Sprintf(" of %d", budget)
		}
		entries = append(entries, activityEntry{
			Message:     message,
			Severity:    activityWarn,
			ReplicaUID:  replica.GetUid(),
			ReplicaName: replica.GetName(),
			At:          at.AsTime(),
		})
	}
	return entries
}

func terminalReplica(replica *deploymentv1.Replica) bool {
	return replica.GetState() == string(loco.ReplicaStopped) || replica.GetState() == string(loco.ReplicaKilled)
}

func replicaActivityMessage(replica *deploymentv1.Replica) (string, string) {
	var message, severity string
	switch loco.ReplicaState(replica.GetState()) {
	case loco.ReplicaScheduling:
		message, severity = "Waiting for a node", activityInfo
	case loco.ReplicaPulling:
		message, severity = "Pulling image", activityInfo
	case loco.ReplicaStarting:
		message, severity = "Starting", activityInfo
	case loco.ReplicaServing:
		return "Serving traffic", activityOK
	case loco.ReplicaFailing:
		message, severity = "Failing", activityError
	case loco.ReplicaStopping:
		message, severity = "Stopping", activityInfo
	case loco.ReplicaStopped:
		message, severity = "Stopped", activityOK
		if code := replica.GetLastExit().GetExitCode(); code != 0 && code != exitSIGTERM {
			severity = activityWarn
		}
	case loco.ReplicaKilled:
		message, severity = "Killed", activityWarn
	case loco.ReplicaUnknown:
		message, severity = "Replica state unknown", activityWarn
	}
	if replica.GetDetail() != "" {
		message += " · " + replica.GetDetail()
	}
	return message, severity
}

func ingestRollout(
	ctx context.Context,
	qtx *genDb.Queries,
	deploymentID uuid.UUID,
	scope activityScope,
	report *agentv1.PlacementStatus,
) error {
	clusterID, revision := *scope.clusterID, scope.revision
	if err := qtx.OpenRollout(
		ctx,
		genDb.OpenRolloutParams{DeploymentID: deploymentID, ClusterID: clusterID, Revision: revision},
	); err != nil {
		return fmt.Errorf("open rollout: %w", err)
	}
	snapshot, loadErr := qtx.LockRollout(ctx, genDb.LockRolloutParams{
		DeploymentID: deploymentID, ClusterID: clusterID, Revision: revision,
	})
	if loadErr != nil {
		return fmt.Errorf("lock rollout: %w", loadErr)
	}
	var previous rolloutSnapshot
	if err := json.Unmarshal(snapshot, &previous); err != nil {
		return fmt.Errorf("decode rollout snapshot: %w", err)
	}
	if previous.PlacementID == "" && report.GetPlacementId() != "" {
		if err := seedRollout(ctx, qtx, &previous, report.GetPlacementId(), revision); err != nil {
			return err
		}
	}
	entries := diffRollout(previous, report)
	next := rolloutSnapshot{
		PlacementID:     report.GetPlacementId(),
		Replicas:        report.GetReplicas(),
		Problem:         report.GetProblem(),
		DesiredReplicas: report.GetDesiredReplicas(),
		Count:           previous.Count,
		Started:         startedReplicas(previous.Started, report),
	}
	if err := writeDeploymentActivity(ctx, qtx, deploymentID, scope, entries); err != nil {
		return err
	}
	next.Count += int32(len(entries))
	if len(entries) == 0 && previous.PlacementID == next.PlacementID &&
		previous.DesiredReplicas == next.DesiredReplicas &&
		proto.Equal(previous.Problem, next.Problem) &&
		maps.Equal(previous.Started, next.Started) &&
		slices.EqualFunc(
			previous.Replicas,
			next.Replicas,
			func(a, b *deploymentv1.Replica) bool { return proto.Equal(a, b) },
		) {
		return nil
	}
	data, encodeErr := json.Marshal(next)
	if encodeErr != nil {
		return fmt.Errorf("encode rollout snapshot: %w", encodeErr)
	}
	if err := qtx.StoreRollout(
		ctx,
		genDb.StoreRolloutParams{DeploymentID: deploymentID, ClusterID: clusterID, Revision: revision, Snapshot: data},
	); err != nil {
		return fmt.Errorf("store rollout: %w", err)
	}
	if err := qtx.NotifyDeploymentActivity(ctx, deploymentID.String()); err != nil {
		return fmt.Errorf("notify activity: %w", err)
	}
	return nil
}

// seedRollout fills a revision's empty snapshot with the replicas and replica count the placement's
// previous revision last reported, so its first report records only what changed since.
func seedRollout(
	ctx context.Context,
	qtx *genDb.Queries,
	snapshot *rolloutSnapshot,
	placementID string,
	revision int64,
) error {
	data, err := qtx.GetPreviousPlacementRollout(
		ctx,
		genDb.GetPreviousPlacementRolloutParams{PlacementID: placementID, Revision: revision},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read previous rollout: %w", err)
	}
	var seed rolloutSnapshot
	if err := json.Unmarshal(data, &seed); err != nil {
		return fmt.Errorf("decode previous rollout snapshot: %w", err)
	}
	snapshot.Replicas, snapshot.DesiredReplicas, snapshot.Started = seed.Replicas, seed.DesiredReplicas, seed.Started
	return nil
}

func capturePlacementRollout(
	ctx context.Context,
	qtx *genDb.Queries,
	clusterID uuid.UUID,
	placement genDb.UpdatePlacementStatusRow,
	report *agentv1.PlacementStatus,
) (genDb.Deployment, error) {
	var id uuid.UUID
	if report.GetDeploymentId() == "" {
		id = *placement.DeploymentID
	} else {
		var err error
		id, err = uuid.Parse(report.GetDeploymentId())
		if err != nil {
			return genDb.Deployment{}, fmt.Errorf("reported deployment id: %w", err)
		}
	}
	deployment, err := qtx.GetDeploymentByID(ctx, id)
	if err != nil {
		return genDb.Deployment{}, fmt.Errorf("read reported deployment: %w", err)
	}
	notOwned := fmt.Errorf("reported deployment %s does not belong to placement %s", id, report.GetPlacementId())
	if deployment.ResourceID != placement.ResourceID || deployment.EnvironmentID != placement.EnvironmentID {
		return genDb.Deployment{}, notOwned
	}
	if _, err := qtx.GetDeploymentPlacement(ctx, genDb.GetDeploymentPlacementParams{
		DeploymentID: id, ClusterID: clusterID,
	}); errors.Is(err, pgx.ErrNoRows) {
		return genDb.Deployment{}, notOwned
	} else if err != nil {
		return genDb.Deployment{}, fmt.Errorf("read reported deployment placement: %w", err)
	}
	scope := activityScope{clusterID: &clusterID, region: placement.Region, revision: report.GetObservedRevision()}
	if err := ingestRollout(ctx, qtx, id, scope, report); err != nil {
		return genDb.Deployment{}, err
	}
	return deployment, nil
}

func validRolloutReport(ctx context.Context, report *agentv1.PlacementStatus) bool {
	for _, replica := range report.GetReplicas() {
		if !slices.Contains(loco.ReplicaStates, loco.ReplicaState(replica.GetState())) {
			slog.WarnContext(
				ctx,
				"ignoring rollout with invalid replica state",
				"placement_id",
				report.GetPlacementId(),
				"state",
				replica.GetState(),
			)
			return false
		}
	}
	if problem := report.GetProblem(); problem != nil {
		if _, valid := loco.ProblemMessage(loco.ProblemReason(problem.GetReason())); !valid {
			slog.WarnContext(
				ctx,
				"ignoring rollout with invalid problem reason",
				"placement_id",
				report.GetPlacementId(),
			)
			return false
		}
	}
	return true
}

// activityScope is what activity entries are about: one region's placement revision, or the
// whole deployment when clusterID is nil.
type activityScope struct {
	clusterID *uuid.UUID
	region    string
	revision  int64
}

func writeDeploymentActivity(
	ctx context.Context,
	qtx *genDb.Queries,
	deploymentID uuid.UUID,
	scope activityScope,
	entries []activityEntry,
) error {
	if len(entries) == 0 {
		return nil
	}
	now := time.Now()
	count := int64(len(entries))

	last, err := qtx.AllocateActivitySeq(ctx, genDb.AllocateActivitySeqParams{DeploymentID: deploymentID, Count: count})
	if err != nil {
		return fmt.Errorf("allocate activity sequence: %w", err)
	}
	seq := last - count
	for _, entry := range entries {
		at := entry.At
		if at.IsZero() {
			at = now
		}
		seq++
		if err := qtx.InsertDeploymentActivity(
			ctx,
			genDb.InsertDeploymentActivityParams{
				DeploymentID: deploymentID,
				Seq:          seq,
				Revision:     scope.revision,
				At:           at,
				Severity:     entry.Severity,
				ReplicaUid:   entry.ReplicaUID,
				ReplicaName:  entry.ReplicaName,
				Message:      entry.Message,
				ClusterID:    scope.clusterID,
				Region:       scope.region,
			},
		); err != nil {
			return fmt.Errorf("insert activity: %w", err)
		}
	}
	return qtx.NotifyDeploymentActivity(ctx, deploymentID.String())
}

func problemReportMessage(problem *agentv1.ProblemReport) string {
	message, _ := loco.ProblemMessage(loco.ProblemReason(problem.GetReason()))
	if problem.GetDetail() != "" {
		message += " " + problem.GetDetail()
	}
	return message
}
