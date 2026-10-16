package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func TestDeploymentPreservesTheRenameThatCommitsWhileItWaits(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeploymentWith(t, f, sameRegionSpec, imageService()); err != nil {
		t.Fatal(err)
	}
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	ctx := context.WithValue(t.Context(), contextkeys.EntityScopesKey, scopes)
	release := sync.OnceFunc(f.holdResourceLock(t))
	t.Cleanup(release)
	const renamed = "renamed-service"
	updated := make(chan error, 1)
	go func() {
		_, err := server.UpdateResource(
			ctx,
			&resourcev1.UpdateResourceRequest{ResourceId: f.resourceID.String(), Name: new(renamed)},
		)
		updated <- err
	}()
	f.waitForLockWaiters(t, 1)
	created := make(chan error, 1)
	go func() { _, err := deployService(t, f, imageService()); created <- err }()
	f.waitForLockWaiters(t, 2)
	release()
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	if err := <-created; err != nil {
		t.Fatal(err)
	}
	resource, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	desired := desiredPayload(t, f)
	if resource.Name != renamed || desired.ResourceName != renamed || desired.AppSpec.ResourceName != renamed {
		t.Fatalf(
			"resource name=%q, payload name=%q, application name=%q; want %q",
			resource.Name,
			desired.ResourceName,
			desired.AppSpec.ResourceName,
			renamed,
		)
	}
}

func TestDeploymentMergesTheResourceSpecThatCommitsWhileItWaits(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeploymentWith(t, f, sameRegionSpec, imageService()); err != nil {
		t.Fatal(err)
	}
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
	if _, err := lockWorkspaceEnvironments(ctx, qtx, resource.WorkspaceID); err != nil {
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
	f.waitForLockWaiters(t, 1)
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
