package service

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

func TestDeleteUserRefusesAWorkspaceGrantThatCommitsWhileItWaits(t *testing.T) {
	f := newDeployFixture(t)
	ctx := t.Context()
	var userID uuid.UUID
	err := f.pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ('doomed@example.com') RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatal(err)
	}
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
	if _, err = qtx.LockUserForMembership(ctx, userID); err != nil {
		t.Fatal(err)
	}
	server := NewUserServer(f.pool, f.queries, nil, nil)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeUser, EntityID: userID, Scope: genDb.ScopeAdmin},
	}
	deleteCtx := context.WithValue(ctx, contextkeys.EntityScopesKey, scopes)
	deleted := make(chan error, 1)
	go func() {
		_, deleteErr := server.DeleteUser(deleteCtx, &userv1.DeleteUserRequest{UserId: userID.String()})
		deleted <- deleteErr
	}()
	f.waitForLockWaiters(t, 1)
	err = qtx.AddUserScope(ctx, genDb.AddUserScopeParams{
		UserID:     userID,
		Scope:      genDb.ScopeRead,
		EntityType: genDb.EntityTypeWorkspace,
		EntityID:   f.workspaceID(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wantCode(t, <-deleted, connect.CodeFailedPrecondition)
}
