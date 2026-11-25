package service

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	stoppedRestarts = 6
	stoppedDetail   = "exit 1 · 6 restarts"
	stoppedReplica  = "web-stopped"
	timeoutDetail   = "not healthy within 10 min · 0/1 nodes are available: insufficient memory"
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

// imageDeployment deploys the image service to the fixture's region.
func imageDeployment(t *testing.T, f *deployFixture) uuid.UUID {
	t.Helper()
	response, err := createDeploymentWith(t, f, sameRegionSpec, imageService())
	if err != nil {
		t.Fatal(err)
	}
	return uuid.MustParse(response.GetDeploymentId())
}

// liveDeployment is the deployment the fixture cluster's placement runs.
func (f *deployFixture) liveDeployment(t *testing.T) uuid.UUID {
	t.Helper()
	p := f.placement(t, f.clusterID)
	if p.DeploymentID == nil {
		t.Fatalf("placement %s runs no deployment", p.ID)
	}
	return *p.DeploymentID
}

func stoppedReport() *agentv1.PlacementStatus {
	return &agentv1.PlacementStatus{
		Stopped: &agentv1.StoppedRevision{
			Reason:      agentv1.StopReason_STOP_REASON_RESTARTS_EXHAUSTED,
			ReplicaName: stoppedReplica,
			Restarts:    stoppedRestarts,
		},
		Problem: &agentv1.ProblemReport{
			Reason: string(loco.ProblemRestartsExhausted), ReplicaName: stoppedReplica, Detail: stoppedDetail,
		},
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
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true, Deployed: true})

	f.reportStatus(t, &agentv1.PlacementStatus{Available: true})
	if got := f.placementStatus(t, id); got != genDb.PlacementStatusDegraded {
		t.Fatalf("placement after a restart = %s, want degraded", got)
	}
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusDegraded {
		t.Fatalf("deployment after a restart = %s, want degraded", got)
	}
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true, Deployed: true})
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
	if data[events.FieldRestarts] != float64(stoppedRestarts) ||
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
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true, Deployed: true})
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusRunning {
		t.Fatalf("deployment after the restart rolled out = %s, want running", got)
	}
}

func TestARolloutStoppedForRestartsFails(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	id := f.deployAs(t, genDb.DeploymentActorTypeUser, f.userID)
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true, Deployed: true})
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

func TestARolloutStoppedAtItsDeployTimeoutFailsWithoutAFallback(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	id := f.deployAs(t, genDb.DeploymentActorTypeUser, f.userID)
	f.reportStatus(t, &agentv1.PlacementStatus{
		Problem: &agentv1.ProblemReport{Reason: string(loco.ProblemUnschedulable), Detail: "insufficient memory"},
	})
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusDeploying {
		t.Fatalf("deployment waiting for a node = %s, want deploying", got)
	}
	f.reportStatus(t, &agentv1.PlacementStatus{
		Stopped: &agentv1.StoppedRevision{Reason: agentv1.StopReason_STOP_REASON_DEPLOY_TIMEOUT},
		Problem: &agentv1.ProblemReport{Reason: string(loco.ProblemDeployTimeout), Detail: timeoutDetail},
	})
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusFailed {
		t.Fatalf("deployment = %s, want failed", got)
	}
	failed := f.eventsOf(t, events.DeploymentFailed, id)
	message, _ := loco.ProblemMessage(loco.ProblemDeployTimeout)
	if len(failed) != 1 || failed[0][events.FieldMessage] != message+" "+timeoutDetail {
		t.Fatalf("deployment.failed events = %v, want one with the timeout", failed)
	}
	if _, counted := failed[0][events.FieldRestarts]; counted {
		t.Fatalf("a deploy timeout recorded restarts: %v", failed[0])
	}
	if crashed := f.eventsOf(t, events.DeploymentCrashed, id); len(crashed) != 0 {
		t.Fatalf("a deploy timeout recorded deployment.crashed: %v", crashed)
	}
}

func TestAStartupFailureFailsWithoutAFallback(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	id := f.deployAs(t, genDb.DeploymentActorTypeUser, f.userID)
	report := &agentv1.PlacementStatus{
		Stopped: &agentv1.StoppedRevision{Reason: agentv1.StopReason_STOP_REASON_STARTUP_FAILED},
		Problem: &agentv1.ProblemReport{Reason: string(loco.ProblemImageUser), Detail: "image will run as root"},
	}
	f.reportStatus(t, report)
	f.reportStatus(t, report)
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusFailed {
		t.Fatalf("deployment = %s, want failed", got)
	}
	failed := f.eventsOf(t, events.DeploymentFailed, id)
	message, _ := loco.ProblemMessage(loco.ProblemImageUser)
	if len(failed) != 1 || failed[0][events.FieldMessage] != message+" image will run as root" {
		t.Fatalf("deployment.failed events = %v, want one with the startup failure", failed)
	}
	if _, counted := failed[0][events.FieldRestarts]; counted {
		t.Fatalf("startup failure recorded restarts: %v", failed[0])
	}
	if crashed := f.eventsOf(t, events.DeploymentCrashed, id); len(crashed) != 0 {
		t.Fatalf("startup failure recorded deployment.crashed: %v", crashed)
	}
}

func TestARunningPlacementStartsOnceItsRevisionIsDeployed(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	started := func(id uuid.UUID) bool {
		t.Helper()
		placement, err := f.queries.GetDeploymentPlacement(t.Context(), genDb.GetDeploymentPlacementParams{
			DeploymentID: id, ClusterID: f.clusterID,
		})
		if err != nil {
			t.Fatal(err)
		}
		deployment, err := f.queries.GetDeploymentByID(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if (placement.StartedAt != nil) != (deployment.StartedAt != nil) {
			t.Fatalf("placement started %v, deployment started %v", placement.StartedAt, deployment.StartedAt)
		}
		return placement.StartedAt != nil
	}
	rollbackTarget := func(id uuid.UUID) *uuid.UUID {
		t.Helper()
		previous, err := f.queries.PreviousLiveDeploymentForCluster(t.Context(),
			genDb.PreviousLiveDeploymentForClusterParams{DeploymentID: id, ClusterID: f.clusterID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return &previous.ID
	}

	first := f.deployAs(t, genDb.DeploymentActorTypeUser, f.userID)
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true})
	if got := f.placementStatus(t, first); got != genDb.PlacementStatusRunning {
		t.Fatalf("healthy placement = %s, want running", got)
	}
	if started(first) {
		t.Fatal("a placement not yet deployed started")
	}
	if ready := f.eventsOf(t, events.DeploymentReady, first); len(ready) != 0 {
		t.Fatalf("deployment.ready events = %d before the revision was deployed", len(ready))
	}
	second := f.deployAs(t, genDb.DeploymentActorTypeUser, f.userID)
	if target := rollbackTarget(second); target != nil {
		t.Fatalf("rollback target = %s, want none before a deployment was deployed", target)
	}
	if got := f.deploymentStatus(t, first); got != genDb.DeploymentStatusCanceled {
		t.Fatalf("replaced deployment that was never deployed = %s, want canceled", got)
	}

	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true})
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true, Deployed: true})
	if !started(second) {
		t.Fatal("a deployed placement did not start")
	}
	if ready := f.eventsOf(t, events.DeploymentReady, second); len(ready) != 1 {
		t.Fatalf("deployment.ready events = %d, want 1 once deployed", len(ready))
	}
	third := f.deployAs(t, genDb.DeploymentActorTypeUser, f.userID)
	if target := rollbackTarget(third); target == nil || *target != second {
		t.Fatalf("rollback target = %v, want the deployed %s", target, second)
	}
}

func TestAStartedDeploymentServesUntilItsReplacementIsDeployed(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	first := f.deployAs(t, genDb.DeploymentActorTypeUser, f.userID)
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true, Deployed: true})
	second := f.deployAs(t, genDb.DeploymentActorTypeUser, f.userID)
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true})
	if got := f.placementStatus(t, second); got != genDb.PlacementStatusRunning {
		t.Fatalf("replacement = %s, want running", got)
	}
	if got := f.placementStatus(t, first); got != genDb.PlacementStatusRunning {
		t.Fatalf("previous deployment before its replacement was deployed = %s, want running", got)
	}
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true, Deployed: true})
	if got := f.placementStatus(t, first); got != genDb.PlacementStatusSuperseded {
		t.Fatalf("previous deployment once its replacement was deployed = %s, want superseded", got)
	}
}

func TestARestartRecordsReadyOnceTheRestartedRevisionIsDeployed(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	id := f.deployAs(t, genDb.DeploymentActorTypeUser, f.userID)
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true, Deployed: true})
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true})
	for range 3 {
		f.reportStatus(t, stoppedReport())
	}
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusCrashed {
		t.Fatalf("deployment = %s, want crashed", got)
	}
	if err := restartResource(t, f, f.envID, "", genDb.ScopeWrite); err != nil {
		t.Fatal(err)
	}
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true})
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusRunning {
		t.Fatalf("restarted deployment = %s, want running", got)
	}
	if ready := f.eventsOf(t, events.DeploymentReady, id); len(ready) != 1 {
		t.Fatalf("deployment.ready events = %d before the restarted revision was deployed, want 1", len(ready))
	}
	f.reportStatus(t, &agentv1.PlacementStatus{Available: true, Healthy: true, Deployed: true})
	if ready := f.eventsOf(t, events.DeploymentReady, id); len(ready) != 2 {
		t.Fatalf("deployment.ready events = %d once the restarted revision was deployed, want 2", len(ready))
	}
}

// twoRegionDeployment applies a two-region worker and returns its deployment, the second region's
// cluster, and a report of the worker's placement on a cluster.
func (f *deployFixture) twoRegionDeployment(t *testing.T) (uuid.UUID, uuid.UUID, func(uuid.UUID, bool)) {
	t.Helper()
	f.prepareApply(t)
	other := f.addOtherRegionCluster(t)
	id := uuid.MustParse(
		applyOK(t, f, planFileHeader+applyFileTwoRegions, applyOptions{}).GetDeployments()[0].GetDeploymentId())
	worker, _ := f.resourceByName(t, applyWorker)
	report := func(cluster uuid.UUID, deployed bool) {
		t.Helper()
		p := f.placementOf(t, worker.ID, cluster)
		f.agentServer().recordStatus(t.Context(), cluster, 0, &agentv1.PlacementStatus{
			PlacementId: p.ID.String(), ObservedRevision: p.DesiredRevision,
			Available: true, Healthy: true, Deployed: deployed, ReadyReplicas: 1,
		})
	}
	return id, other, report
}

func TestDeploymentReadyWaitsForEveryRegionToDeploy(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	id, other, report := f.twoRegionDeployment(t)
	report(f.clusterID, true)
	report(other, false)
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusRunning {
		t.Fatalf("deployment = %s, want running", got)
	}
	if ready := f.eventsOf(t, events.DeploymentReady, id); len(ready) != 0 {
		t.Fatalf("deployment.ready events = %v while %s is not deployed", ready, otherRegion)
	}
	report(other, true)
	ready := f.eventsOf(t, events.DeploymentReady, id)
	if len(ready) != 1 {
		t.Fatalf("deployment.ready events = %v, want 1 once every region deployed", ready)
	}
	if regions, ok := ready[0][events.FieldRegions].([]any); !ok || len(regions) != 2 {
		t.Fatalf("deployment.ready regions = %v, want both", ready[0][events.FieldRegions])
	}
}

func TestADeploymentTurnsReadyWhenItsUndeployedRegionIsReplaced(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	id, other, report := f.twoRegionDeployment(t)
	report(f.clusterID, true)
	report(other, false)
	applyOK(t, f, planFileHeader+twoRegionsBiggerCPU, applyOptions{})
	if ready := f.eventsOf(t, events.DeploymentReady, id); len(ready) != 1 {
		t.Fatalf("deployment.ready events = %v, want 1 once its only serving region is deployed", ready)
	}
	deployment, err := f.queries.GetDeploymentByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if deployment.StartedAt == nil {
		t.Fatal("a ready deployment has no start")
	}
}
