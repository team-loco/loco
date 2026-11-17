package service

import (
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func TestDeploymentRolloutLifecycle(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	server := f.agentServer()
	first, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.deploymentStatus(t, first); got != genDb.DeploymentStatusDeploying {
		t.Fatalf("created=%s", got)
	}
	p := f.placement(t, f.clusterID)
	report := &agentv1.PlacementStatus{PlacementId: p.ID.String(), ObservedRevision: p.DesiredRevision, Available: true}
	server.recordStatus(ctx, f.clusterID, 0, report)
	second, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.deploymentStatus(t, first); got != genDb.DeploymentStatusRunning {
		t.Fatalf("previous=%s", got)
	}
	p = f.placement(t, f.clusterID)
	server.markApplied(ctx, f.clusterID, p.ID, p.DesiredRevision)
	report.ObservedRevision = p.DesiredRevision
	report.Available = false
	report.ProgressDeadlineExceeded = true
	server.recordStatus(ctx, f.clusterID, 0, report)
	if got := f.deploymentStatus(t, second); got != genDb.DeploymentStatusFailed {
		t.Fatalf("deadline=%s", got)
	}
	if got := f.deploymentStatus(t, first); got != genDb.DeploymentStatusRunning {
		t.Fatalf("previous after failure=%s", got)
	}
	failedPlacement := f.placement(t, f.clusterID)
	if failedPlacement.DesiredRevision != p.DesiredRevision || failedPlacement.DesiredDeleted ||
		string(failedPlacement.DesiredSpec) != string(p.DesiredSpec) || *failedPlacement.DeploymentID != second {
		t.Fatal("failed rollout changed the application sent to the cluster")
	}
	pending, err := f.queries.ListPendingPlacements(ctx, f.clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("failed rollout queued cluster changes: %v", pending)
	}
	report.Available = true
	report.ProgressDeadlineExceeded = false
	server.recordStatus(ctx, f.clusterID, 0, report)
	if got := f.deploymentStatus(t, second); got != genDb.DeploymentStatusRunning {
		t.Fatalf("recovered=%s", got)
	}
	if got := f.deploymentStatus(t, first); got != genDb.DeploymentStatusSuperseded {
		t.Fatalf("previous after replacement=%s", got)
	}
	third, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatal(err)
	}
	if got := f.deploymentStatus(t, third); got != genDb.DeploymentStatusCanceled {
		t.Fatalf("never ran=%s", got)
	}
}

func TestDeploymentProblemLifecycle(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	server := f.agentServer()
	id, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatal(err)
	}
	p := f.placement(t, f.clusterID)
	report := &agentv1.PlacementStatus{PlacementId: p.ID.String(), ObservedRevision: p.DesiredRevision,
		Problem: &agentv1.ProblemReport{Reason: "crash_loop", Detail: "exit 1"}}
	server.recordStatus(ctx, f.clusterID, 0, report)
	var detail string
	if err := f.pool.QueryRow(ctx, `SELECT snapshot->'problem'->>'detail' FROM deployment_rollouts
 WHERE deployment_id=$1 ORDER BY revision DESC LIMIT 1`, id).
		Scan(&detail); err != nil ||
		detail != "exit 1" {
		t.Fatalf("problem detail=%q %v", detail, err)
	}
	report.Available = true
	server.recordStatus(ctx, f.clusterID, 0, report)
	server.recordStatus(ctx, f.clusterID, 0, report)
	report.Problem = nil
	server.recordStatus(ctx, f.clusterID, 0, report)
	server.recordStatus(ctx, f.clusterID, 0, report)
	var cleared bool
	if err := f.pool.QueryRow(ctx, `SELECT snapshot->'problem' IS NULL FROM deployment_rollouts
 WHERE deployment_id=$1 ORDER BY revision DESC LIMIT 1`, id).
		Scan(&cleared); err != nil ||
		!cleared {
		t.Fatalf("clear=%v %v", cleared, err)
	}
	for _, kind := range []string{"deployment.ready", "resource.degraded", "resource.recovered"} {
		var count int
		if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE resource_id=$1 AND type=$2`, f.resourceID, kind).
			Scan(&count); err != nil ||
			count != 1 {
			t.Fatalf("%s count=%d %v", kind, count, err)
		}
	}
	report.Available = false
	report.ProgressDeadlineExceeded = true
	server.recordStatus(ctx, f.clusterID, 1, report)
	report.ObservedRevision--
	server.recordStatus(ctx, f.clusterID, 0, report)
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusRunning {
		t.Fatalf("stale changed status=%s", got)
	}
	report.ObservedRevision = p.DesiredRevision
	server.recordStatus(ctx, f.clusterID, 0, report)
	server.recordStatus(ctx, f.clusterID, 0, report)
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE resource_id=$1 AND type='deployment.failed'`,
		f.resourceID).
		Scan(&count); err != nil ||
		count != 1 {
		t.Fatalf("failed count=%d %v", count, err)
	}
}
