package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
)

// beginGate opens a transaction the test holds locks in, rolled back at cleanup unless committed.
func beginGate(t *testing.T, f *deployFixture) (pgx.Tx, *genDb.Queries) {
	t.Helper()
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
	return gate, genDb.New(gate)
}

func TestSuspendingAnOrganizationWaitsForABuildReport(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	if _, err := f.deploy(f.ctx, staticSpec); err != nil {
		t.Fatal(err)
	}
	buildID := uuid.MustParse(f.queued(t))
	deployBuild(t, f, buildID.String())

	// The gate locks in recordBuildStatus's order: resource, build, then the resource's placements.
	ctx := context.Background()
	gate, qgate := beginGate(t, f.deployFixture)
	if err := qgate.LockBuildResource(ctx, buildID); err != nil {
		t.Fatal(err)
	}
	if _, err := qgate.LockBuild(ctx, buildID); err != nil {
		t.Fatal(err)
	}

	suspended := make(chan error, 1)
	go func() {
		_, err := f.suspend(t, testSuspensionReason)
		suspended <- err
	}()
	dbtest.WaitForLockWaiters(t, f.pool, 1)
	if _, err := gate.Exec(
		ctx, `UPDATE placements SET updated_at = NOW() WHERE resource_id = $1`, f.resourceID,
	); err != nil {
		t.Fatalf("build report: %v", err)
	}
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("build report: %v", err)
	}
	if err := <-suspended; err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if got := f.row(t, buildID.String()).Status; got != genDb.BuildStatusCanceled {
		t.Fatalf("build status = %s, want it canceled by the suspension", got)
	}
}

func TestSuspendAndResumeWaitForAStatusReportThatRollsBack(t *testing.T) {
	t.Parallel()
	for name, suspended := range map[string]bool{"suspending": false, "resuming": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newDeployFixture(t)
			rollbackDeployment(t, f, true, nil)
			reportRollback(t, f, true, false)
			failed := rollbackDeployment(t, f, true, nil)
			if suspended {
				f.mustSuspend(t)
			}

			// The gate locks in recordStatus's order: resource, its placement, then fails the deployment.
			ctx := context.Background()
			gate, qgate := beginGate(t, f)
			if _, err := qgate.LockResources(ctx, []uuid.UUID{f.resourceID}); err != nil {
				t.Fatal(err)
			}
			if _, err := gate.Exec(
				ctx, `UPDATE placements SET updated_at = NOW() WHERE resource_id = $1`, f.resourceID,
			); err != nil {
				t.Fatal(err)
			}

			changed := make(chan error, 1)
			go func() {
				var changeErr error
				if suspended {
					_, changeErr = f.resume(t)
				} else {
					_, changeErr = f.suspend(t, testSuspensionReason)
				}
				changed <- changeErr
			}()
			dbtest.WaitForLockWaiters(t, f.pool, 1)
			err := advancePlacementStatus(ctx, qgate, failed, f.clusterID, genDb.PlacementStatusFailed,
				placementStatusNames(placementLiveStatuses...), transitionCause{message: testApplyFailure},
				testServiceDefaults())
			// A rollback started while the organization is still suspended is refused.
			if suspended != errors.Is(err, ErrOrgSuspended) || !suspended && err != nil {
				t.Fatalf("status report: %v", err)
			}
			if err == nil {
				err = gate.Commit(ctx)
			} else {
				err = gate.Rollback(ctx)
			}
			if err != nil {
				t.Fatalf("status report: %v", err)
			}
			if err = <-changed; err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
}
