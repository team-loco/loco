package service

import (
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func TestDeploymentTimestampsFollowClusterReports(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	id, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatal(err)
	}
	read := func() genDb.Deployment {
		t.Helper()
		row, readErr := f.queries.GetDeploymentByID(ctx, id)
		if readErr != nil {
			t.Fatal(readErr)
		}
		return row
	}
	p := f.placement(t, f.clusterID)
	server := f.agentServer()
	report := &agentv1.PlacementStatus{PlacementId: p.ID.String(), ObservedRevision: p.DesiredRevision}
	server.recordStatus(ctx, f.clusterID, 0, report)
	if got := read(); got.StartedAt != nil || got.CompletedAt != nil {
		t.Fatalf("deploying timestamps=%+v", got)
	}
	report.Available = true
	server.recordStatus(ctx, f.clusterID, 0, report)
	running := read()
	if running.StartedAt == nil || running.StartedAt.Before(running.CreatedAt) {
		t.Fatalf("running start=%v", running.StartedAt)
	}
	report.Available = false
	report.ProgressDeadlineExceeded = true
	server.recordStatus(ctx, f.clusterID, 0, report)
	failed := read()
	if failed.CompletedAt == nil || !failed.StartedAt.Equal(*running.StartedAt) {
		t.Fatalf("failed timestamps=%+v", failed)
	}
	server.recordStatus(ctx, f.clusterID, 0, report)
	if got := read(); !got.CompletedAt.Equal(*failed.CompletedAt) {
		t.Fatal("replay changed completion time")
	}
	report.Available = true
	report.ProgressDeadlineExceeded = false
	server.recordStatus(ctx, f.clusterID, 0, report)
	if got := read(); got.CompletedAt != nil || !got.StartedAt.Equal(*running.StartedAt) {
		t.Fatalf("recovered timestamps=%+v", got)
	}
	if _, err := f.queries.RestartResourcePlacements(
		ctx,
		genDb.RestartResourcePlacementsParams{ResourceID: f.resourceID, EnvironmentID: f.envID},
	); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Status != genDb.DeploymentStatusDeploying {
		t.Fatalf("restart=%s", got.Status)
	}
	p = f.placement(t, f.clusterID)
	report.ObservedRevision = p.DesiredRevision
	report.Available = false
	server.recordStatus(ctx, f.clusterID, 0, report)
	if got := read(); got.Status != genDb.DeploymentStatusDeploying {
		t.Fatalf("waiting restart=%s", got.Status)
	}
	report.Available = true
	server.recordStatus(ctx, f.clusterID, 0, report)
	if got := read(); got.Status != genDb.DeploymentStatusRunning || !got.StartedAt.Equal(*running.StartedAt) {
		t.Fatalf("restarted=%+v", got)
	}
}
