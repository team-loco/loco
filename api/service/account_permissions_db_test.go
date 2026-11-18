package service

import (
	"errors"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/internal/rpc"
)

func TestAccountChangesRequireSession(t *testing.T) {
	t.Parallel()
	a := newMembershipAPI(t)
	admin := a.signUp(t, "admin")
	system := genDb.EntityScope{EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin}
	if err := a.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
		UserID: admin.id, EntityType: system.EntityType, EntityID: system.EntityID, Scope: system.Scope,
	}); err != nil {
		t.Fatal(err)
	}
	user := a.signUp(t, "user")
	other := a.signUp(t, "other")
	userToken, _ := administrativeToken(t, a, user, genDb.Entity{Type: genDb.EntityTypeUser, ID: user.id},
		genDb.EntityScope{EntityType: genDb.EntityTypeUser, EntityID: user.id, Scope: genDb.ScopeAdmin})
	adminToken, _ := administrativeToken(t, a, admin, genDb.Entity{Type: genDb.EntityTypeUser, ID: admin.id}, system)
	deleteUser := func(caller *rpc.Clients, target membershipUser) error {
		_, err := caller.User.DeleteUser(t.Context(), &userv1.DeleteUserRequest{UserId: target.id.String()})
		return err
	}
	setStatus := func(caller *rpc.Clients, target membershipUser, status userv1.UserStatus) error {
		_, err := caller.User.SetUserStatus(t.Context(), &userv1.SetUserStatusRequest{
			Key: &userv1.SetUserStatusRequest_UserId{UserId: target.id.String()}, Status: status,
		})
		return err
	}
	suspended, active := userv1.UserStatus_USER_STATUS_SUSPENDED, userv1.UserStatus_USER_STATUS_ACTIVE

	wantSessionRequired(t, deleteUser(userToken, user))
	wantSessionRequired(t, deleteUser(adminToken, other))
	wantSessionRequired(t, setStatus(adminToken, other, suspended))
	wantSessionRequired(t, setStatus(adminToken, other, active))
	for _, target := range []membershipUser{user, other} {
		if _, err := a.queries.GetUserByID(t.Context(), target.id); err != nil {
			t.Fatalf("refused token request changed the account: %v", err)
		}
	}

	if err := deleteUser(user.clients, other); codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("session deletes another account = %v, want permission denied", err)
	}
	if err := setStatus(user.clients, other, suspended); codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("session suspends without system admin = %v, want permission denied", err)
	}
	for _, status := range []userv1.UserStatus{suspended, active} {
		if err := setStatus(admin.clients, other, status); err != nil {
			t.Fatalf("system admin session sets status %s: %v", status, err)
		}
	}
	if err := deleteUser(admin.clients, other); err != nil {
		t.Fatalf("system admin session deletes another account: %v", err)
	}
	if err := deleteUser(user.clients, user); err != nil {
		t.Fatalf("session deletes its own account: %v", err)
	}
	for _, target := range []membershipUser{user, other} {
		if _, err := a.queries.GetUserByID(t.Context(), target.id); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("deleted account lookup = %v", err)
		}
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
