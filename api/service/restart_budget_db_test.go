package service

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	stoppedRestarts = 6
	stoppedWindow   = 600
	stoppedDetail   = "exit 1 · 6 restarts in 10 min"
	stoppedReplica  = "web-stopped"
)

func (f *deployFixture) eventsOf(t *testing.T, kind string, id uuid.UUID) []map[string]any {
	t.Helper()
	rows, err := f.pool.Query(t.Context(),
		`SELECT data FROM events WHERE type = $1 AND subject_type = $2 AND subject_id = $3 ORDER BY seq`,
		kind, events.SubjectDeployment, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatal(err)
		}
		out = append(out, data)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *deployFixture) reportStatus(t *testing.T, report *agentv1.PlacementStatus) {
	t.Helper()
	placement := f.placement(t, f.clusterID)
	report.PlacementId = placement.ID.String()
	report.ObservedRevision = placement.DesiredRevision
	f.agentServer().recordStatus(t.Context(), f.clusterID, 0, report)
}

func stoppedReport() *agentv1.PlacementStatus {
	return &agentv1.PlacementStatus{
		Stopped: &agentv1.StoppedRevision{ReplicaName: stoppedReplica, Restarts: stoppedRestarts},
		Problem: &agentv1.ProblemReport{
			Reason: string(loco.ProblemRestartsExhausted), ReplicaName: stoppedReplica, Detail: stoppedDetail,
		},
		MaxRestarts:          5,
		RestartWindowSeconds: stoppedWindow,
	}
}

func (f *deployFixture) placementStatus(t *testing.T, id uuid.UUID) genDb.PlacementStatus {
	t.Helper()
	placement, err := f.queries.GetDeploymentPlacement(t.Context(), genDb.GetDeploymentPlacementParams{
		DeploymentID: id, ClusterID: f.clusterID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return placement.Status
}

func TestARunningServiceStoppedForRestartsCrashesOnceAndRestartsAgain(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	id := f.deployAs(t, genDb.DeploymentActorTypeUser, f.userID)
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true})

	f.reportStatus(t, &agentv1.PlacementStatus{Available: true})
	if got := f.placementStatus(t, id); got != genDb.PlacementStatusDegraded {
		t.Fatalf("placement after a restart = %s, want degraded", got)
	}
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusDegraded {
		t.Fatalf("deployment after a restart = %s, want degraded", got)
	}
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true})
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusRunning {
		t.Fatalf("deployment after a quiet window = %s, want running", got)
	}
	if ready := f.eventsOf(t, events.DeploymentReady, id); len(ready) != 1 {
		t.Fatalf("deployment.ready events = %d, want 1 across a recovery", len(ready))
	}
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true})
	for range 3 {
		f.reportStatus(t, stoppedReport())
	}
	if got := f.placementStatus(t, id); got != genDb.PlacementStatusCrashed {
		t.Fatalf("placement = %s, want crashed", got)
	}
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusCrashed {
		t.Fatalf("deployment = %s, want crashed", got)
	}
	crashed := f.eventsOf(t, events.DeploymentCrashed, id)
	if len(crashed) != 1 {
		t.Fatalf("deployment.crashed events = %d, want 1", len(crashed))
	}
	message, _ := loco.ProblemMessage(loco.ProblemRestartsExhausted)
	data := crashed[0]
	if data[events.FieldRestarts] != float64(stoppedRestarts) || data[events.FieldWindow] != float64(stoppedWindow) ||
		data[events.FieldMessage] != message+" "+stoppedDetail {
		t.Fatalf("deployment.crashed data = %v", data)
	}
	if failed := f.eventsOf(t, events.DeploymentFailed, id); len(failed) != 0 {
		t.Fatalf("a steady-state stop recorded deployment.failed: %v", failed)
	}
	if live := f.liveDeployment(t); live != id {
		t.Fatalf("live deployment = %s, want the crashed %s without a rollback", live, id)
	}

	if err := restartResource(t, f, f.envID, "", genDb.ScopeWrite); err != nil {
		t.Fatal(err)
	}
	if got := f.placementStatus(t, id); got != genDb.PlacementStatusDeploying {
		t.Fatalf("placement after a restart = %s, want deploying", got)
	}
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true})
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusRunning {
		t.Fatalf("deployment after the restart rolled out = %s, want running", got)
	}
}

func TestARolloutStoppedForRestartsFails(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	id := f.deployAs(t, genDb.DeploymentActorTypeUser, f.userID)
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true})
	report := stoppedReport()
	report.Stopped.Rollout = true
	f.reportStatus(t, report)
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusFailed {
		t.Fatalf("deployment = %s, want failed", got)
	}
	failed := f.eventsOf(t, events.DeploymentFailed, id)
	if len(failed) != 1 || failed[0][events.FieldRestarts] != float64(stoppedRestarts) {
		t.Fatalf("deployment.failed events = %v, want one with the restarts", failed)
	}
	if crashed := f.eventsOf(t, events.DeploymentCrashed, id); len(crashed) != 0 {
		t.Fatalf("a stopped rollout recorded deployment.crashed: %v", crashed)
	}
}

func TestRegionStatusReportsTheRestartBudget(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.deployAs(t, genDb.DeploymentActorTypeUser, f.userID)
	server := &ResourceServer{queries: f.queries, defaults: testServiceDefaults()}
	budget := func() int32 {
		t.Helper()
		statuses, err := server.environmentStatuses(t.Context(), []uuid.UUID{f.resourceID})
		if err != nil {
			t.Fatal(err)
		}
		return statuses[f.resourceID][0].GetRegions()[0].GetMaxRestarts()
	}
	if got := budget(); got != testServiceDefaults().MaxRestarts {
		t.Fatalf("budget = %d, want the default %d", got, testServiceDefaults().MaxRestarts)
	}
	if err := f.queries.SetResourceDeployPolicy(t.Context(), genDb.SetResourceDeployPolicyParams{
		ID: f.resourceID, MaxRestarts: new(int32(2)),
	}); err != nil {
		t.Fatal(err)
	}
	if got := budget(); got != 2 {
		t.Fatalf("budget = %d, want the service's 2", got)
	}
}
