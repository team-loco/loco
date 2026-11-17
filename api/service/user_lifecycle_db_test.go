package service

import (
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

func promoteLifecycleAdmin(t *testing.T, o *membershipOrg, user membershipUser) {
	t.Helper()
	o.addOrgMember(t, user)
	if _, err := o.owner.clients.Org.SetOrgMemberRole(t.Context(), &orgv1.SetOrgMemberRoleRequest{
		OrgId: o.orgID, UserId: user.id.String(), Role: orgv1.OrgRole_ORG_ROLE_ADMIN,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteUserRequiresAnotherActiveAdmin(t *testing.T) {
	t.Parallel()
	o := newMembershipOrg(t)
	other := o.api.signUp(t, "other")
	promoteLifecycleAdmin(t, o, other)
	if _, err := o.api.queries.SetUserStatus(t.Context(), genDb.SetUserStatusParams{
		ID: other.id, Status: genDb.UserStatusSuspended,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := o.owner.clients.User.DeleteUser(t.Context(), &userv1.DeleteUserRequest{UserId: o.owner.id.String()})
	if codeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), membershipOrgName) {
		t.Fatalf("delete with suspended successor = %v, want precondition naming organization", err)
	}
	if _, err := o.api.queries.GetUserByID(t.Context(), o.owner.id); err != nil {
		t.Fatalf("refused deletion changed account: %v", err)
	}
	if _, err := o.api.queries.SetUserStatus(t.Context(), genDb.SetUserStatusParams{
		ID: other.id, Status: genDb.UserStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.owner.clients.User.DeleteUser(
		t.Context(),
		&userv1.DeleteUserRequest{UserId: o.owner.id.String()},
	); err != nil {
		t.Fatalf("delete with active successor: %v", err)
	}
	if _, err := o.api.queries.GetOrgByID(t.Context(), uuid.MustParse(o.orgID)); err != nil {
		t.Fatalf("creator deletion removed organization: %v", err)
	}
}

func TestConcurrentAccountDeletionKeepsAnActiveAdmin(t *testing.T) {
	t.Parallel()
	o := newMembershipOrg(t)
	other := o.api.signUp(t, "other")
	promoteLifecycleAdmin(t, o, other)
	tx, err := o.api.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rollbackOnCleanup(t, tx)
	if _, err := genDb.New(tx).LockOrgForMembership(t.Context(), uuid.MustParse(o.orgID)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	results := make([]error, 2)
	for i, user := range []membershipUser{o.owner, other} {
		wg.Go(func() {
			_, results[i] = user.clients.User.DeleteUser(
				t.Context(),
				&userv1.DeleteUserRequest{UserId: user.id.String()},
			)
		})
	}
	dbtest.WaitForLockWaiters(t, o.api.pool, len(results))
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	var succeeded, refused int
	for _, err := range results {
		switch {
		case err == nil:
			succeeded++
		case codeOf(err) == connect.CodeFailedPrecondition:
			refused++
		default:
			t.Fatalf("concurrent deletion: %v", results)
		}
	}
	if succeeded != 1 || refused != 1 {
		t.Fatalf("deletion results = %v, want one success and one last-admin refusal", results)
	}
}

func TestMembershipMutationReloadsActorAfterWaiting(t *testing.T) {
	t.Parallel()
	o := newMembershipOrg(t)
	other := o.api.signUp(t, "other")
	o.addOrgMember(t, other)
	tx, err := o.api.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rollbackOnCleanup(t, tx)
	qtx := genDb.New(tx)
	if _, err := qtx.LockUserForMembership(t.Context(), o.owner.id); err != nil {
		t.Fatal(err)
	}
	changed := make(chan error, 1)
	go func() {
		_, err := o.owner.clients.Org.SetOrgMemberRole(t.Context(), &orgv1.SetOrgMemberRoleRequest{
			OrgId: o.orgID, UserId: other.id.String(), Role: orgv1.OrgRole_ORG_ROLE_ADMIN,
		})
		changed <- err
	}()
	dbtest.WaitForLockWaiters(t, o.api.pool, 1)
	if err := qtx.RemoveUserScope(t.Context(), genDb.RemoveUserScopeParams{
		UserID: o.owner.id, EntityType: genDb.EntityTypeOrganization,
		EntityID: uuid.MustParse(o.orgID), Scope: genDb.ScopeAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-changed; codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("mutation by revoked actor = %v, want PermissionDenied", err)
	}
}
