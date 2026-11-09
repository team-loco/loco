package service

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
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
	deleteCtx := reqctx.WithPrincipal(ctx, reqctx.Principal{Scopes: scopes})
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

func TestDeleteUserReportsBlockingReferences(t *testing.T) {
	f := newDeployFixture(t)
	workspaceID := f.workspaceID(t)
	server := NewUserServer(f.pool, f.queries, nil, nil)

	for _, tt := range []struct {
		name    string
		insert  string
		message string
	}{
		{
			name: "workspace",
			insert: `INSERT INTO workspaces (org_id, name, created_by)
			           SELECT org_id, 'other', $1 FROM workspaces WHERE id = $2`,
			message: "user created workspaces",
		},
		{
			name:    "environment",
			insert:  `INSERT INTO environments (workspace_id, name, created_by) VALUES ($2, 'other', $1)`,
			message: "user created environments",
		},
		{
			name: "token",
			insert: `INSERT INTO api_tokens (token_hash, name, entity_type, entity_id, scopes, created_by, expires_at)
			         VALUES ('test-hash', 'test-token', 'workspace', $2, '[]', $1, now() + interval '1 hour')`,
			message: "user created API tokens",
		},
		{
			name: "sent invitation",
			insert: `INSERT INTO invitations (org_id, email, invited_by, expires_at)
			           SELECT org_id, 'sent@example.com', $1, now() FROM workspaces WHERE id = $2`,
			message: "user sent invitations",
		},
		{
			name: "claimed invitation",
			insert: `INSERT INTO invitations (org_id, email, invited_by, expires_at, claimed_by, claimed_at)
			         SELECT org_id, 'claimed@example.com', created_by, now(), $1, now() FROM workspaces WHERE id = $2`,
			message: "user claimed invitations",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			var userID uuid.UUID
			if err := f.pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`,
				uuid.NewString()+"@example.com").Scan(&userID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(ctx, tt.insert, userID, workspaceID); err != nil {
				t.Fatal(err)
			}
			ctx = reqctx.WithPrincipal(ctx, reqctx.Principal{Scopes: []genDb.EntityScope{
				{EntityType: genDb.EntityTypeUser, EntityID: userID, Scope: genDb.ScopeAdmin},
			}})

			_, err := server.DeleteUser(ctx, &userv1.DeleteUserRequest{UserId: userID.String()})
			wantCode(t, err, connect.CodeFailedPrecondition)
			if public := publicError(err); public.Message() != tt.message {
				t.Fatalf("DeleteUser error = %v, want %q", err, tt.message)
			}
		})
	}
}
