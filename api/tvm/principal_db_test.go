package tvm_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/db/dbtest"
	queries "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
)

type principalFixture struct {
	pool                 *pgxpool.Pool
	q                    *queries.Queries
	machine              *tvm.VendingMachine
	user, org, workspace uuid.UUID
}

func newPrincipalFixture(t *testing.T) principalFixture {
	t.Helper()
	f := principalFixture{pool: dbtest.NewPool(t)}
	f.q = queries.New(f.pool)
	f.machine = tvm.NewVendingMachine(f.q, testConfig())
	owner, err := f.q.CreateUser(t.Context(), queries.CreateUserParams{Email: "owner@principal.test"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := f.q.CreateUser(t.Context(), queries.CreateUserParams{Email: "issuer@principal.test"})
	if err != nil {
		t.Fatal(err)
	}
	f.user = user.ID
	org, err := f.q.CreateOrganization(
		t.Context(),
		queries.CreateOrganizationParams{Name: "principal", CreatedBy: owner.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	f.org = org.ID
	f.workspace, err = f.q.CreateWorkspace(
		t.Context(),
		queries.CreateWorkspaceParams{OrgID: org.ID, Name: "target", CreatedBy: owner.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.q.AddUserScope(
		t.Context(),
		queries.AddUserScopeParams{
			UserID:     f.user,
			EntityType: queries.EntityTypeOrganization,
			EntityID:   f.org,
			Scope:      queries.ScopeRead,
		},
	); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f principalFixture) issue(t *testing.T, owner queries.Entity) (string, queries.CreateAPITokenRow) {
	t.Helper()
	scope := queries.EntityScope{EntityType: queries.EntityTypeOrganization, EntityID: f.org, Scope: queries.ScopeRead}
	if owner.Type != queries.EntityTypeUser {
		scope.EntityType, scope.EntityID = owner.Type, owner.ID
	}
	token, row, err := f.machine.Issue(
		t.Context(),
		uuid.NewString(),
		f.user,
		owner,
		[]queries.EntityScope{scope},
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	return token, row
}

func TestPersonalTokenFollowsLiveUserGrants(t *testing.T) {
	t.Parallel()
	f := newPrincipalFixture(t)
	ctx := t.Context()
	token, _ := f.issue(t, queries.Entity{Type: queries.EntityTypeUser, ID: f.user})
	principal, err := f.machine.GetToken(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if principal.UserID != f.user {
		t.Fatalf("principal user = %s, want %s", principal.UserID, f.user)
	}
	read := queries.EntityScope{
		EntityType: queries.EntityTypeWorkspace,
		EntityID:   f.workspace,
		Scope:      queries.ScopeRead,
	}
	if err = authz.New(f.q).Check(ctx, principal.Scopes, read); err != nil {
		t.Fatal(err)
	}
	write := queries.EntityScope{EntityType: queries.EntityTypeOrganization, EntityID: f.org, Scope: queries.ScopeWrite}
	if err = f.q.AddUserScope(
		ctx,
		queries.AddUserScopeParams{
			UserID:     f.user,
			EntityType: write.EntityType,
			EntityID:   write.EntityID,
			Scope:      write.Scope,
		},
	); err != nil {
		t.Fatal(err)
	}
	principal, err = f.machine.GetToken(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if err = authz.New(f.q).Check(ctx, principal.Scopes, write); !errors.Is(err, authz.ErrInsufficientPermissions) {
		t.Fatalf("gained grant expanded token ceiling: %v", err)
	}
	if err = f.q.RemoveUserScope(
		ctx,
		queries.RemoveUserScopeParams{
			UserID:     f.user,
			EntityType: queries.EntityTypeOrganization,
			EntityID:   f.org,
			Scope:      queries.ScopeRead,
		},
	); err != nil {
		t.Fatal(err)
	}
	principal, err = f.machine.GetToken(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if len(principal.Scopes) != 0 {
		t.Fatalf("removed human grant left token scopes: %v", principal.Scopes)
	}
	if _, err = f.q.SetUserStatus(
		ctx,
		queries.SetUserStatusParams{ID: f.user, Status: queries.UserStatusSuspended},
	); err != nil {
		t.Fatal(err)
	}
	if _, err = f.machine.GetToken(ctx, token); !errors.Is(err, tvm.ErrInvalidExpiredToken) {
		t.Fatalf("suspended personal token = %v", err)
	}
	if err = f.q.DeleteUser(ctx, f.user); err != nil {
		t.Fatal(err)
	}
	if _, err = f.machine.GetToken(ctx, token); !errors.Is(err, tvm.ErrInvalidExpiredToken) {
		t.Fatalf("deleted personal token = %v", err)
	}
	var tokens int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM api_tokens").Scan(&tokens); err != nil {
		t.Fatal(err)
	}
	if tokens != 0 {
		t.Fatalf("personal tokens after account deletion = %d", tokens)
	}
}

func TestOwnedTokenHasNoHumanPrincipalAndSurvivesIssuerDeletion(t *testing.T) {
	t.Parallel()
	f := newPrincipalFixture(t)
	ctx := t.Context()
	token, row := f.issue(t, queries.Entity{Type: queries.EntityTypeWorkspace, ID: f.workspace})
	principal, err := f.machine.GetToken(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if principal.UserID != uuid.Nil || principal.ActorID != row.ID {
		t.Fatalf("owned token identity = user %s, actor %s", principal.UserID, principal.ActorID)
	}
	if principal.Entity.Type != queries.EntityTypeWorkspace || principal.Entity.ID != f.workspace {
		t.Fatalf("token scope owner = %v", principal.Entity)
	}
	if err = f.q.DeleteUser(ctx, f.user); err != nil {
		t.Fatal(err)
	}
	principal, err = f.machine.GetToken(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	scope := queries.EntityScope{
		EntityType: queries.EntityTypeWorkspace,
		EntityID:   f.workspace,
		Scope:      queries.ScopeRead,
	}
	if err = authz.New(f.q).Check(ctx, principal.Scopes, scope); err != nil {
		t.Fatal(err)
	}
	var users, grants int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM user_scopes").Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if users != 1 || grants != 0 {
		t.Fatalf("owned token left users=%d grants=%d", users, grants)
	}
	if err = f.machine.Revoke(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err = f.machine.GetToken(ctx, token); !errors.Is(err, tvm.ErrInvalidExpiredToken) {
		t.Fatalf("revoked token = %v", err)
	}
}

func TestOwnedTokenCannotOutliveWorkspace(t *testing.T) {
	t.Parallel()
	f := newPrincipalFixture(t)
	token, _ := f.issue(t, queries.Entity{Type: queries.EntityTypeWorkspace, ID: f.workspace})
	if err := f.q.DeleteWorkspace(t.Context(), f.workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := f.machine.GetToken(t.Context(), token); !errors.Is(err, tvm.ErrInvalidExpiredToken) {
		t.Fatalf("deleted token owner = %v", err)
	}
}

func TestPersonalTokenRejectsExpiry(t *testing.T) {
	t.Parallel()
	f := newPrincipalFixture(t)
	token, row := f.issue(t, queries.Entity{Type: queries.EntityTypeUser, ID: f.user})
	if _, err := f.pool.Exec(
		t.Context(),
		"UPDATE api_tokens SET expires_at = now() - interval '1 second' WHERE id = $1",
		row.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.machine.GetToken(t.Context(), token); !errors.Is(err, tvm.ErrInvalidExpiredToken) {
		t.Fatalf("expired personal token = %v", err)
	}
}
