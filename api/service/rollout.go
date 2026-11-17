package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	rolloutCauseDeploy   = "deploy"
	rolloutCauseRestart  = "restart"
	rolloutCauseConfig   = "config"
	rolloutCauseRollback = "rollback"
	activityInfo         = "info"
	activityOK           = "ok"
	activityWarn         = "warn"
	activityError        = "error"
	rolloutActivityLimit = 500
	activityLimitMessage = "Activity limit reached"
)

type rolloutSnapshot struct {
	Replicas        []*agentv1.ReplicaReport `json:"replicas,omitempty"`
	Problem         *agentv1.ProblemReport   `json:"problem,omitempty"`
	DesiredReplicas int32                    `json:"desiredReplicas,omitempty"`
	Count           int32                    `json:"-"`
}

type activityEntry struct {
	Message     string
	Severity    string
	ReplicaUID  string
	ReplicaName string
	At          time.Time
	Problem     bool
}

func diffRollout(previous rolloutSnapshot, report *agentv1.PlacementStatus) []activityEntry {
	old := make(map[string]*agentv1.ReplicaReport, len(previous.Replicas))
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
		if before := old[uid]; before != nil && before.GetState() == replica.GetState() {
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
		entries = append(entries, activityEntry{Message: message, Severity: severity, Problem: true})
	}
	if hadPrevious && !livePrevious {
		entries = append(entries, activityEntry{Message: "Previous version gone", Severity: activityOK})
	}
	count := previous.Count
	var limited []activityEntry
	for _, entry := range entries {
		if count == rolloutActivityLimit {
			limited = append(limited, activityEntry{Message: activityLimitMessage, Severity: activityWarn})
			count++
		}
		if count > rolloutActivityLimit && !entry.Problem {
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

func terminalReplica(replica *agentv1.ReplicaReport) bool {
	return replica.GetState() == string(loco.ReplicaStopped) || replica.GetState() == string(loco.ReplicaKilled)
}

func replicaActivityMessage(replica *agentv1.ReplicaReport) (string, string) {
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
		if replica.ExitCode != nil && replica.GetExitCode() != 0 {
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
	revision int64,
	report *agentv1.PlacementStatus,
) error {
	for _, replica := range report.GetReplicas() {
		if !slices.Contains(loco.ReplicaStates, loco.ReplicaState(replica.GetState())) {
			slog.WarnContext(
				ctx,
				"ignoring rollout with invalid replica state",
				"deployment_id",
				deploymentID,
				"state",
				replica.GetState(),
			)
			return nil
		}
	}
	if problem := report.GetProblem(); problem != nil {
		if _, valid := loco.ProblemMessage(loco.ProblemReason(problem.GetReason())); !valid {
			slog.WarnContext(ctx, "ignoring rollout with invalid problem reason", "deployment_id", deploymentID)
			return nil
		}
	}
	inserted, openErr := qtx.OpenRollout(
		ctx,
		genDb.OpenRolloutParams{DeploymentID: deploymentID, Revision: revision, Cause: rolloutCauseConfig},
	)
	if openErr != nil {
		return fmt.Errorf("open rollout: %w", openErr)
	}
	if inserted != 0 {
		slog.WarnContext(ctx, "report opened a missing rollout", "deployment_id", deploymentID, "revision", revision)
	}
	row, loadErr := qtx.LockRollout(ctx, genDb.LockRolloutParams{DeploymentID: deploymentID, Revision: revision})
	if loadErr != nil {
		return fmt.Errorf("lock rollout: %w", loadErr)
	}
	var previous rolloutSnapshot
	if !bytes.Equal(row.Replicas, []byte("[]")) {
		if err := json.Unmarshal(row.Replicas, &previous); err != nil {
			return fmt.Errorf("decode rollout snapshot: %w", err)
		}
	}
	previous.Count = row.ActivityCount
	entries := diffRollout(previous, report)
	now := time.Now()
	next := rolloutSnapshot{
		Replicas:        report.GetReplicas(),
		Problem:         report.GetProblem(),
		DesiredReplicas: report.GetDesiredReplicas(),
		Count:           previous.Count,
	}
	count := int64(len(entries))

	seq := int64(0)
	if count > 0 {
		last, err := qtx.AllocateActivitySeq(
			ctx,
			genDb.AllocateActivitySeqParams{DeploymentID: deploymentID, Count: count},
		)
		if err != nil {
			return fmt.Errorf("allocate activity sequence: %w", err)
		}
		seq = last - count
	}
	var finished *time.Time
	for _, entry := range entries {
		at := entry.At
		if at.IsZero() {
			at = now
		}
		seq++
		next.Count++
		if err := qtx.InsertDeploymentActivity(
			ctx,
			genDb.InsertDeploymentActivityParams{
				DeploymentID: deploymentID,
				Seq:          seq,
				Revision:     revision,
				At:           at,
				Severity:     entry.Severity,
				ReplicaUid:   entry.ReplicaUID,
				ReplicaName:  entry.ReplicaName,
				Message:      entry.Message,
			},
		); err != nil {
			return fmt.Errorf("insert activity: %w", err)
		}
	}
	// Finishing a rollout is independent of the activity row limit.
	if hasPrevious(previous.Replicas, revision) && !hasPrevious(report.GetReplicas(), revision) {
		finished = &now
	}
	if len(entries) == 0 && previous.DesiredReplicas == next.DesiredReplicas &&
		proto.Equal(previous.Problem, next.Problem) &&
		slices.EqualFunc(
			previous.Replicas,
			next.Replicas,
			func(a, b *agentv1.ReplicaReport) bool { return proto.Equal(a, b) },
		) {
		return nil
	}
	data, encodeErr := json.Marshal(next)
	if encodeErr != nil {
		return fmt.Errorf("encode rollout snapshot: %w", encodeErr)
	}
	if err := qtx.StoreRollout(
		ctx,
		genDb.StoreRolloutParams{
			DeploymentID:    deploymentID,
			Revision:        revision,
			Snapshot:        data,
			DesiredReplicas: next.DesiredReplicas,
			ActivityCount:   next.Count,
			FinishedAt:      finished,
		},
	); err != nil {
		return fmt.Errorf("store rollout: %w", err)
	}
	if err := qtx.NotifyDeploymentActivity(ctx, deploymentID.String()); err != nil {
		return fmt.Errorf("notify activity: %w", err)
	}
	return nil
}

func hasPrevious(replicas []*agentv1.ReplicaReport, revision int64) bool {
	for _, replica := range replicas {
		if replica.GetPlacementRevision() < revision && !terminalReplica(replica) {
			return true
		}
	}
	return false
}

func openPlacementRollout(ctx context.Context, qtx *genDb.Queries, placementID uuid.UUID, cause string) error {
	if err := qtx.OpenPlacementRollout(
		ctx,
		genDb.OpenPlacementRolloutParams{PlacementID: placementID, Cause: cause},
	); err != nil {
		return fmt.Errorf("open placement rollout: %w", err)
	}
	return nil
}
