package service

import (
	"testing"
	"time"

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

func TestReplacingDeploymentsFinalizesTimestamps(t *testing.T) {
	t.Parallel()
	for _, status := range []genDb.DeploymentStatus{
		genDb.DeploymentStatusBuilding,
		genDb.DeploymentStatusDeploying,
		genDb.DeploymentStatusRunning,
		genDb.DeploymentStatusFailed,
	} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			f := newDeployFixture(t)
			ctx := t.Context()
			previous, err := f.deploy(ctx, staticSpec)
			if err != nil {
				t.Fatal(err)
			}
			var started, completed *time.Time
			prior := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
			if status == genDb.DeploymentStatusRunning || status == genDb.DeploymentStatusFailed {
				started = &prior
			}
			if status == genDb.DeploymentStatusFailed {
				completed = &prior
			}
			if _, err = f.pool.Exec(
				ctx,
				"UPDATE deployments SET status=$2, started_at=$3, completed_at=$4 WHERE id=$1",
				previous,
				status,
				started,
				completed,
			); err != nil {
				t.Fatal(err)
			}
			if _, err = f.deploy(ctx, staticSpec); err != nil {
				t.Fatal(err)
			}
			if status == genDb.DeploymentStatusRunning {
				old, readErr := f.queries.GetDeploymentByID(ctx, previous)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if old.Status != status || old.CompletedAt != nil {
					t.Fatalf("still-serving deployment = %+v", old)
				}
				f.finishRollouts(t)
			}
			old, err := f.queries.GetDeploymentByID(ctx, previous)
			if err != nil {
				t.Fatal(err)
			}
			want := genDb.DeploymentStatusCanceled
			if status == genDb.DeploymentStatusRunning {
				want = genDb.DeploymentStatusSuperseded
			}
			if status == genDb.DeploymentStatusFailed {
				want = status
			}
			if old.Status != want || old.IsActive || old.CompletedAt == nil {
				t.Fatalf("finalized deployment = %+v, want inactive %s with completion time", old, want)
			}
			if completed != nil && !old.CompletedAt.Equal(*completed) {
				t.Fatalf("completion = %v, want preserved %v", old.CompletedAt, completed)
			}
		})
	}
}
