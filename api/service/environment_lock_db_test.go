package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	genDb "github.com/team-loco/loco/api/gen/db"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

// holdEnvironmentLock locks the environment as a write to it does, until the test ends.
func (f *deployFixture) holdEnvironmentLock(t *testing.T, environmentID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}
	t.Cleanup(func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Errorf("release writer: %v", rollbackErr)
		}
	})
	if _, err = lockEnvironment(ctx, genDb.New(tx), environmentID); err != nil {
		t.Fatalf("lock environment: %v", err)
	}
}

func TestDeployWaitsOnlyForWritesToItsEnvironment(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		sameEnv bool
	}{
		{name: "write to another environment", sameEnv: false},
		{name: "write to the same environment", sameEnv: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newDeployFixture(t)
			seedDeployment(t, f, sameRegionSpec)
			stagingID := f.addStagingEnvironment(t)
			f.useLockTimeout(t)
			held := stagingID
			if tc.sameEnv {
				held = f.envID
			}
			f.holdEnvironmentLock(t, held)

			build := &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: testPublicImage}
			_, err := deployService(t, f, &deploymentv1.ServiceDeploymentSpec{Build: build, Port: new(int32(8080))})
			if tc.sameEnv {
				if !isPgLockNotAvailable(err) {
					t.Fatalf("deploy = %v, want a lock timeout", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("deploy: %v", err)
			}
		})
	}
}

func TestStatusReportDoesNotWaitForAWriteToItsEnvironment(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	f.useLockTimeout(t)
	f.holdEnvironmentLock(t, f.envID)

	f.reportRollout(t, true)

	if got := f.placement(t, f.clusterID).ReadyReplicas; got != 1 {
		t.Fatalf("ready replicas = %d, want the reported 1", got)
	}
}
