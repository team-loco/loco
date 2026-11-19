package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func TestFinishingABuildWaitsForTheResourceLockOfANewDeployment(t *testing.T) {
	t.Parallel()
	for name, finish := range map[string]func(*buildFixture, genDb.Build) error{
		"canceled": func(f *buildFixture, build genDb.Build) error {
			_, err := f.cancel(build.ID.String())
			return err
		},
		"succeeded": func(f *buildFixture, build genDb.Build) error {
			status := buildStatus(build.ID.String(), agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED)
			status.ImageDigest = testImageDigest
			return f.agentServer().recordBuildStatus(f.ctx, *build.ClusterID, build.ID, status)
		},
		"lost by its cluster": func(f *buildFixture, build genDb.Build) error {
			return f.agentServer().failClusterBuilds(f.ctx, genDb.FailClusterBuildsParams{
				ClusterID:      build.ClusterID,
				PreviousStatus: genDb.BuildStatusQueued,
				Held:           []uuid.UUID{},
				Message:        buildMissingMessage,
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newBuildFixture(t)
			buildID := f.queued(t)
			deploymentID := deployBuild(t, f, buildID)
			build := f.row(t, buildID)

			// The gate locks in createDeployment's order: resource, superseded placements, deployment.
			ctx := context.Background()
			gate, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if rollbackErr := gate.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
					t.Errorf("release gate: %v", rollbackErr)
				}
			})
			qgate := genDb.New(gate)
			if _, err = qgate.LockResources(ctx, []uuid.UUID{f.resourceID}); err != nil {
				t.Fatal(err)
			}
			if _, err = gate.Exec(
				ctx, `UPDATE deployment_placements SET updated_at = NOW() WHERE deployment_id = $1`, deploymentID,
			); err != nil {
				t.Fatal(err)
			}

			finished := make(chan error, 1)
			go func() { finished <- finish(f, build) }()
			dbtest.WaitForLockWaiters(t, f.pool, 1)
			if err = qgate.LockDeployment(ctx, deploymentID); err != nil {
				t.Fatalf("new deployment: %v", err)
			}
			if err = gate.Commit(ctx); err != nil {
				t.Fatalf("new deployment: %v", err)
			}
			if err = <-finished; err != nil {
				t.Fatalf("finish build: %v", err)
			}
			if got := f.deploymentStatus(t, deploymentID); got == genDb.DeploymentStatusBuilding {
				t.Fatalf("deployment status = %s after its build finished", got)
			}
		})
	}
}
