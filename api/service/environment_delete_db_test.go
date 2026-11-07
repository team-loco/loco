package service

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
)

func TestDeleteEnvironmentRefusesADeploymentThatCommitsWhileItWaits(t *testing.T) {
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
	if _, err = lockWorkspaceEnvironments(ctx, qtx, workspaceID); err != nil {
		t.Fatal(err)
	}
	server := NewEnvironmentServer(f.pool, f.queries, nil)
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
	f.waitForLockWaiters(t, 1)
	if _, err = qtx.CreateDeployment(ctx, f.paramsFor(f.clusterID)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wantCode(t, <-deleted, connect.CodeFailedPrecondition)
}
