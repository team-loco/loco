package service

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func TestDeleteEnvironmentRefusesADeploymentThatCommitsWhileItWaits(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	workspaceID := f.workspaceID(t)
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Errorf("rollback: %v", rollbackErr)
		}
	})
	qtx := f.queries.WithTx(tx)
	if err = lockWorkspaceEnvironments(ctx, qtx, workspaceID); err != nil {
		t.Fatal(err)
	}
	server := NewEnvironmentServer(f.pool, f.queries, limits.Defaults())
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeAdmin},
	}
	deleteCtx := reqctx.WithPrincipal(ctx, reqctx.Principal{Scopes: scopes})
	deleted := make(chan error, 1)
	go func() {
		_, deleteErr := server.DeleteEnvironment(
			deleteCtx,
			&environmentv1.DeleteEnvironmentRequest{EnvironmentId: f.envID.String()},
		)
		deleted <- deleteErr
	}()
	dbtest.WaitForLockWaiters(t, f.pool, 1)
	if _, err = qtx.CreateDeployment(ctx, f.paramsFor(f.clusterID).CreateDeploymentParams); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wantCode(t, <-deleted, connect.CodeFailedPrecondition)
}

func TestDeletingTheLastConfiguredEnvironmentKeepsTheResourceDiscoverable(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	staging := f.addStagingEnvironment(t)
	workspaceID := f.workspaceID(t)
	ctx := f.tenant.owner(t)
	environments := NewEnvironmentServer(f.pool, f.queries, limits.Defaults())
	if _, err := environments.DeleteEnvironment(ctx, &environmentv1.DeleteEnvironmentRequest{
		EnvironmentId: f.envID.String(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.queries.GetResourceByID(t.Context(), f.resourceID); err != nil {
		t.Fatalf("deleting environment removed shared identity: %v", err)
	}
	configurations, err := f.queries.ListResourceEnvironments(t.Context(), []uuid.UUID{f.resourceID})
	if err != nil || len(configurations) != 0 {
		t.Fatalf("remaining configurations = %v (%v), want none", configurations, err)
	}
	resources := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	listed, err := resources.ListWorkspaceResources(ctx, &resourcev1.ListWorkspaceResourcesRequest{
		WorkspaceId: workspaceID.String(), EnvironmentId: staging.String(), EnvironmentScoped: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.GetResources()) != 1 || listed.GetResources()[0].GetId() != f.resourceID.String() {
		t.Fatalf("unconfigured shared identity disappeared from dashboard: %v", listed.GetResources())
	}
	if len(listed.GetResources()[0].GetEnvironments()) != 0 {
		t.Fatal("unconfigured identity borrowed another environment's settings")
	}
	f.markClustersHealthy(t)
	if _, err = deployServiceIn(t, f, imageService(), staging); err != nil {
		t.Fatalf("explicit deployment could not configure the existing identity: %v", err)
	}
	configurations, err = f.queries.ListResourceEnvironments(t.Context(), []uuid.UUID{f.resourceID})
	if err != nil || len(configurations) != 1 || configurations[0].EnvironmentID != staging {
		t.Fatalf("explicit deployment configurations = %v (%v), want staging only", configurations, err)
	}
}
