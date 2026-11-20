package service

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	"github.com/team-loco/loco/api/tvm"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

func TestDeleteUserRemovesAWorkspaceGrantThatCommitsWhileItWaits(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	var userID uuid.UUID
	err := f.pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ('doomed@example.com') RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.queries.AddUserScope(ctx, genDb.AddUserScopeParams{
		UserID: userID, EntityType: genDb.EntityTypeUser, EntityID: userID, Scope: genDb.ScopeAdmin,
	}); err != nil {
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
	server := NewUserServer(f.pool, f.queries, nil)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeUser, EntityID: userID, Scope: genDb.ScopeAdmin},
	}
	deleteCtx := reqctx.WithPrincipal(
		ctx,
		reqctx.Principal{
			Scopes: scopes, UserID: userID, Method: genDb.DeploymentMethodCli,
			ActorType: genDb.DeploymentActorTypeUser, ActorID: userID,
			Entity: genDb.Entity{Type: genDb.EntityTypeUser, ID: userID},
		},
	)
	deleted := make(chan error, 1)
	go func() {
		_, deleteErr := server.DeleteUser(deleteCtx, &userv1.DeleteUserRequest{UserId: userID.String()})
		deleted <- deleteErr
	}()
	dbtest.WaitForLockWaiters(t, f.pool, 1)
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
	if err := <-deleted; err != nil {
		t.Fatalf("delete user after grant: %v", err)
	}
	var count int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM user_scopes WHERE user_id = $1", userID).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("grant survived account deletion")
	}
}

func TestDeleteUserKeepsHistoricalReferences(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	workspaceID := f.workspaceID(t)
	server := NewUserServer(f.pool, f.queries, nil)

	for _, tt := range []struct {
		name      string
		insert    string
		reference string
	}{
		{
			name: "workspace",
			insert: `INSERT INTO workspaces (org_id, name, created_by)
			           SELECT org_id, 'other', $1 FROM workspaces WHERE id = $2`,
			reference: "workspaces.created_by",
		},
		{
			name: "environment",
			insert: `INSERT INTO environments (workspace_id, slug, name, created_by)
			           VALUES ($2, 'other', 'other', $1)`,
			reference: "environments.created_by",
		},
		{
			name: "token",
			insert: `INSERT INTO api_tokens (display_suffix, token_hash, name, entity_type, entity_id,
 scopes, created_by, expires_at)
			         VALUES ('test', 'test-hash', 'test-token', 'workspace', $2, '[]', $1,
 now() + interval '1 hour')`,
			reference: "api_tokens.created_by",
		},
		{
			name: "sent invitation",
			insert: `INSERT INTO invitations (org_id, email, role, invited_by, expires_at)
			           SELECT org_id, 'sent@example.com', 'member', $1, now() FROM workspaces WHERE id = $2`,
			reference: "invitations.invited_by",
		},
		{
			name: "claimed invitation",
			insert: `INSERT INTO invitations (org_id, email, role, invited_by, expires_at, claimed_by, claimed_at)
			         SELECT org_id, 'claimed@example.com', 'member', created_by, now(), $1, now() FROM workspaces WHERE id = $2`,
			reference: "invitations.claimed_by",
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
			if err := f.queries.AddUserScope(ctx, genDb.AddUserScopeParams{
				UserID: userID, EntityType: genDb.EntityTypeUser, EntityID: userID, Scope: genDb.ScopeAdmin,
			}); err != nil {
				t.Fatal(err)
			}
			ctx = reqctx.WithPrincipal(
				ctx,
				reqctx.Principal{
					UserID:    userID,
					Method:    genDb.DeploymentMethodCli,
					ActorType: genDb.DeploymentActorTypeUser,
					ActorID:   userID,
					Entity:    genDb.Entity{Type: genDb.EntityTypeUser, ID: userID},
					Scopes: []genDb.EntityScope{
						{EntityType: genDb.EntityTypeUser, EntityID: userID, Scope: genDb.ScopeAdmin},
					},
				},
			)

			_, err := server.DeleteUser(ctx, &userv1.DeleteUserRequest{UserId: userID.String()})
			if err != nil {
				t.Fatal(err)
			}
			table, column, _ := strings.Cut(tt.reference, ".")
			var preserved int
			if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE "+column+" = $1", userID).
				Scan(&preserved); err != nil {
				t.Fatal(err)
			}
			if preserved != 1 {
				t.Fatalf("historical references preserved = %d, want 1", preserved)
			}
		})
	}
}

func TestDeleteUserEventCarriesRemovedIdentity(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	var userID uuid.UUID
	if err := f.pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ('leaving@example.com') RETURNING id`).
		Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO identities (user_id, issuer, subject)
		VALUES ($1, 'https://idp.example.com', 'sub-1')`, userID); err != nil {
		t.Fatal(err)
	}
	if err := f.queries.AddUserScope(ctx, genDb.AddUserScopeParams{
		UserID: userID, EntityType: genDb.EntityTypeUser, EntityID: userID, Scope: genDb.ScopeAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	ctx = reqctx.WithPrincipal(
		ctx,
		reqctx.Principal{
			UserID:    userID,
			Method:    genDb.DeploymentMethodCli,
			ActorType: genDb.DeploymentActorTypeUser,
			ActorID:   userID,
			Entity:    genDb.Entity{Type: genDb.EntityTypeUser, ID: userID},
			Scopes: []genDb.EntityScope{
				{EntityType: genDb.EntityTypeUser, EntityID: userID, Scope: genDb.ScopeAdmin},
			},
		},
	)
	server := NewUserServer(f.pool, f.queries, nil)

	if _, err := server.DeleteUser(ctx, &userv1.DeleteUserRequest{UserId: userID.String()}); err != nil {
		t.Fatal(err)
	}

	const recordedQuery = `SELECT COALESCE(
		data->'identity' = '{"issuer": "https://idp.example.com", "subject": "sub-1"}', false)
		FROM events WHERE type = 'user.deleted' AND subject_id = $1`
	var recorded bool
	if err := f.pool.QueryRow(ctx, recordedQuery, userID).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if !recorded {
		t.Fatal("user.deleted does not carry the removed https://idp.example.com/sub-1 identity")
	}
}

func TestDeleteUserRevokesSessions(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	var userID uuid.UUID
	if err := f.pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ('signed-in@example.com') RETURNING id`).
		Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO identities (user_id, issuer, subject)
		VALUES ($1, 'https://idp.example.com', 'sub-2')`, userID); err != nil {
		t.Fatal(err)
	}
	if err := f.queries.AddUserScope(ctx, genDb.AddUserScopeParams{
		UserID: userID, EntityType: genDb.EntityTypeUser, EntityID: userID, Scope: genDb.ScopeAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	machine := tvm.NewVendingMachine(f.queries, tvm.Config{
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: time.Hour,
		LastUsedUpdateInterval:      time.Hour,
	})
	web, _, err := machine.IssueWebSession(ctx, userID, netip.Addr{}, "dashboard")
	if err != nil {
		t.Fatal(err)
	}
	cli, _, err := machine.IssueSession(ctx, userID, netip.Addr{}, "cli")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := machine.GetToken(ctx, cli)
	if err != nil {
		t.Fatal(err)
	}
	server := NewUserServer(f.pool, f.queries, nil)

	deleteCtx := reqctx.WithPrincipal(ctx, principal)
	if _, err := server.DeleteUser(deleteCtx, &userv1.DeleteUserRequest{UserId: userID.String()}); err != nil {
		t.Fatal(err)
	}

	for name, token := range map[string]string{"dashboard": web, "cli": cli} {
		if _, err := machine.GetToken(ctx, token); !errors.Is(err, tvm.ErrInvalidExpiredToken) {
			t.Errorf("%s session after deletion = %v, want %v", name, err, tvm.ErrInvalidExpiredToken)
		}
	}
	var sessions int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM session_tokens WHERE user_id = $1", userID).
		Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 {
		t.Fatalf("%d sessions survived account deletion", sessions)
	}
}
