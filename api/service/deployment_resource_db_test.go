package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
)

func TestDeploymentMergesTheResourceSpecThatCommitsWhileItWaits(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := createDeploymentWith(t, f, sameRegionSpec, imageService()); err != nil {
		t.Fatal(err)
	}
	f.reportRollout(t, true)
	ctx := t.Context()
	resource, err := f.queries.GetResourceByID(ctx, f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rollback: %v", err)
		}
	})
	qtx := f.queries.WithTx(tx)
	if err := lockWorkspaceEnvironments(ctx, qtx, resource.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if err := lockResource(ctx, qtx, f.resourceID); err != nil {
		t.Fatal(err)
	}
	updatedSpec := strings.Replace(sameRegionSpec, "100m", biggerCPU, 1)
	if err := qtx.UpdateResourceSpec(
		ctx,
		genDb.UpdateResourceSpecParams{ID: f.resourceID, Spec: []byte(updatedSpec)},
	); err != nil {
		t.Fatal(err)
	}
	created := make(chan error, 1)
	go func() { _, err := deployService(t, f, imageService()); created <- err }()
	dbtest.WaitForLockWaiters(t, f.pool, 1)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-created; err != nil {
		t.Fatal(err)
	}
	if got := desiredPayload(t, f).AppSpec.ServiceSpec.Resources.CPU; got != biggerCPU {
		t.Fatalf("deployment CPU=%q, want committed resource CPU %q", got, biggerCPU)
	}
}
