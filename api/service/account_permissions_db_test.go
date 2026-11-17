package service

import (
	"errors"
	"net/http"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/internal/rpc"
)

func TestDeleteUserUsesActionPermissions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		otherUser  bool
		system     bool
		owned      bool
		scope      tokenv1.Scope
		wantDenied bool
	}{
		{name: "personal admin", scope: tokenv1.Scope_SCOPE_ADMIN},
		{name: "personal read", scope: tokenv1.Scope_SCOPE_READ, wantDenied: true},
		{name: "another account", otherUser: true, scope: tokenv1.Scope_SCOPE_ADMIN, wantDenied: true},
		{name: "system admin", otherUser: true, system: true, scope: tokenv1.Scope_SCOPE_ADMIN},
		{
			name: "organization-owned credential", otherUser: true, owned: true,
			scope: tokenv1.Scope_SCOPE_ADMIN, wantDenied: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := newMembershipAPI(t)
			actor := a.signUp(t, "actor")
			target := actor
			if tc.otherUser {
				target = a.signUp(t, "target")
			}
			owner := &tokenv1.CreateTokenRequest{
				Name: "account administration", EntityType: tokenv1.EntityType_ENTITY_TYPE_USER,
				EntityId: actor.id.String(), ExpiresInSec: 3600,
				Scopes: []*tokenv1.EntityScope{{
					EntityType: tokenv1.EntityType_ENTITY_TYPE_USER, EntityId: actor.id.String(), Scope: tc.scope,
				}},
			}
			if tc.system {
				if err := a.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
					UserID: actor.id, EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin,
				}); err != nil {
					t.Fatal(err)
				}
				owner.Scopes[0].EntityType = tokenv1.EntityType_ENTITY_TYPE_SYSTEM
				owner.Scopes[0].EntityId = uuid.Nil.String()
			}
			if tc.owned {
				org, err := actor.clients.Org.CreateOrg(t.Context(), &orgv1.CreateOrgRequest{})
				if err != nil {
					t.Fatal(err)
				}
				owner.EntityType = tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION
				owner.EntityId = org.GetOrgId()
				owner.Scopes[0].EntityType = owner.GetEntityType()
				owner.Scopes[0].EntityId = owner.GetEntityId()
			}
			issued, err := actor.clients.Token.CreateToken(t.Context(), owner)
			if err != nil {
				t.Fatal(err)
			}
			clients := rpc.NewHTTPClients(http.DefaultClient, a.url, issued.GetToken())
			_, err = clients.User.DeleteUser(t.Context(), &userv1.DeleteUserRequest{UserId: target.id.String()})
			if tc.wantDenied {
				if codeOf(err) != connect.CodePermissionDenied {
					t.Fatalf("delete user = %v, want permission denied", err)
				}
				if _, lookupErr := a.queries.GetUserByID(t.Context(), target.id); lookupErr != nil {
					t.Fatalf("refused deletion changed the account: %v", lookupErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("authorized deletion = %v", err)
			}
			if _, err := a.queries.GetUserByID(t.Context(), target.id); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("deleted account lookup = %v", err)
			}
		})
	}
}

func TestDeleteUserRechecksPermissionAfterWaitingForTheAccount(t *testing.T) {
	t.Parallel()
	a := newMembershipAPI(t)
	u := a.signUp(t, "leaving")
	var workers sync.WaitGroup
	t.Cleanup(workers.Wait)
	tx, err := a.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rollbackOnCleanup(t, tx)
	qtx := a.queries.WithTx(tx)
	if _, err := qtx.LockUserForMembership(t.Context(), u.id); err != nil {
		t.Fatal(err)
	}
	deleted := make(chan error, 1)
	workers.Go(func() {
		_, deleteErr := u.clients.User.DeleteUser(t.Context(), &userv1.DeleteUserRequest{UserId: u.id.String()})
		deleted <- deleteErr
	})
	dbtest.WaitForLockWaiters(t, a.pool, 1)
	if err := qtx.RemoveUserScope(t.Context(), genDb.RemoveUserScopeParams{
		UserID: u.id, EntityType: genDb.EntityTypeUser, EntityID: u.id, Scope: genDb.ScopeAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("deletion after permission removal = %v, want permission denied", err)
	}
	if _, err := a.queries.GetUserByID(t.Context(), u.id); err != nil {
		t.Fatalf("refused deletion changed account: %v", err)
	}
}
