package service

import (
	"testing"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func TestRollbackToPausedClusterKeepsTheFailure(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	rollbackDeployment(t, f, true, nil)
	reportRollback(t, f, true, false)
	failed := rollbackDeployment(t, f, true, nil)
	placement := f.placement(t, f.clusterID)
	if _, updateErr := f.pool.Exec(
		t.Context(),
		`UPDATE clusters SET is_active=false WHERE id=$1`,
		f.clusterID,
	); updateErr != nil {
		t.Fatal(updateErr)
	}
	reportRollback(t, f, false, true)
	row, err := f.queries.GetDeploymentByID(t.Context(), failed)
	if err != nil || row.Status != genDb.DeploymentStatusFailed || !row.IsActive {
		t.Fatalf("failed deployment=%+v %v", row, err)
	}
	if current := f.placement(
		t,
		f.clusterID,
	); current.DeploymentID == nil || *current.DeploymentID != failed ||
		current.DesiredRevision != placement.DesiredRevision {
		t.Fatalf("placement=%+v", current)
	}
	if _, updateErr := f.pool.Exec(
		t.Context(),
		`UPDATE clusters SET is_active=true WHERE id=$1`,
		f.clusterID,
	); updateErr != nil {
		t.Fatal(updateErr)
	}
	reportRollback(t, f, false, true)
	row, err = f.queries.GetDeploymentByID(t.Context(), failed)
	if err != nil || row.Status != genDb.DeploymentStatusFailed || !row.IsActive {
		t.Fatalf("replayed failure=%+v %v", row, err)
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM events
 WHERE type='deployment.failed' AND subject_id=$1`, failed).Scan(&count); err != nil || count != 1 {
		t.Fatalf("failure events=%d %v", count, err)
	}
}

func TestRollbackWithUnavailableBuildImageKeepsTheFailure(t *testing.T) {
	t.Parallel()
	for _, pruned := range []bool{false, true} {
		t.Run(map[bool]string{false: "deleted", true: "pruned"}[pruned], func(t *testing.T) {
			f := newBuildFixture(t)
			buildID := f.queued(t)
			status := buildStatus(buildID, agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED)
			status.ImageDigest = testImageDigest
			if err := f.agentServer().
				recordBuildStatus(f.ctx, f.clusterID, uuid.MustParse(buildID), status); err != nil {
				t.Fatal(err)
			}
			deployBuild(t, f, buildID)
			reportRollback(t, f.deployFixture, true, false)
			failed := rollbackDeployment(t, f.deployFixture, true, nil)
			if _, updateErr := f.pool.Exec(
				f.ctx,
				`UPDATE builds SET image_deleted_at=NOW(),source_deleted_at=NOW() WHERE id=$1`,
				buildID,
			); updateErr != nil {
				t.Fatal(updateErr)
			}
			if pruned {
				n, err := f.queries.PruneBuildHistory(
					f.ctx,
					genDb.PruneBuildHistoryParams{ResourceID: f.resourceID, Keep: 0},
				)
				if err != nil || n != 1 {
					t.Fatalf("pruned=%d %v", n, err)
				}
			}
			reportRollback(t, f.deployFixture, false, true)
			row, err := f.queries.GetDeploymentByID(f.ctx, failed)
			if err != nil || row.Status != genDb.DeploymentStatusFailed || !row.IsActive {
				t.Fatalf("failed deployment=%+v %v", row, err)
			}
			if current := f.placement(t, f.clusterID); current.DeploymentID == nil || *current.DeploymentID != failed {
				t.Fatalf("placement=%+v", current)
			}
		})
	}
}
