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
	if err := f.pool.QueryRow(ctx, `SELECT problem->>'detail' FROM deployments WHERE id=$1`, id).
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
	if err := f.pool.QueryRow(ctx, `SELECT problem IS NULL FROM deployments WHERE id=$1`, id).
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
