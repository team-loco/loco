package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	genDb "github.com/team-loco/loco/api/gen/db"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func hierarchyScopes(t *testing.T, f *deployFixture) []genDb.AddUserScopeParams {
	t.Helper()
	workspace, err := f.queries.GetWorkspaceByIDQuery(t.Context(), f.workspaceID(t))
	if err != nil {
		t.Fatal(err)
	}
	return []genDb.AddUserScopeParams{
		{
			UserID:     workspace.CreatedBy,
			EntityType: genDb.EntityTypeOrganization,
			EntityID:   workspace.OrgID,
			Scope:      genDb.ScopeRead,
		},
		{
			UserID:     workspace.CreatedBy,
			EntityType: genDb.EntityTypeWorkspace,
			EntityID:   workspace.ID,
			Scope:      genDb.ScopeRead,
		},
		{
			UserID:     workspace.CreatedBy,
			EntityType: genDb.EntityTypeResource,
			EntityID:   f.resourceID,
			Scope:      genDb.ScopeRead,
		},
	}
}

func deleteScopeEntity(ctx context.Context, q genDb.Querier, scope genDb.AddUserScopeParams) error {
	switch scope.EntityType {
	case genDb.EntityTypeOrganization:
		return q.DeleteOrg(ctx, scope.EntityID)
	case genDb.EntityTypeWorkspace:
		return q.RemoveWorkspace(ctx, scope.EntityID)
	case genDb.EntityTypeResource:
		return q.DeleteResource(ctx, scope.EntityID)
	default:
		panic("unexpected test entity type")
	}
}

func scopeCount(t *testing.T, f *deployFixture, scope genDb.AddUserScopeParams) int {
	t.Helper()
	var count int
	err := f.pool.QueryRow(t.Context(),
		"SELECT count(*) FROM user_scopes WHERE user_id=$1 AND entity_type=$2 AND entity_id=$3",
		scope.UserID, scope.EntityType, scope.EntityID,
	).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestEntityDeletionCascadesHierarchyScopes(t *testing.T) {
	for index, kind := range []genDb.EntityType{
		genDb.EntityTypeOrganization,
		genDb.EntityTypeWorkspace,
		genDb.EntityTypeResource,
	} {
		t.Run(string(kind), func(t *testing.T) {
			f := newDeployFixture(t)
			scopes := hierarchyScopes(t, f)
			for _, scope := range scopes {
				if err := f.queries.AddUserScope(t.Context(), scope); err != nil {
					t.Fatal(err)
				}
			}
			self := genDb.AddUserScopeParams{
				UserID:     scopes[0].UserID,
				EntityType: genDb.EntityTypeUser,
				EntityID:   scopes[0].UserID,
				Scope:      genDb.ScopeRead,
			}
			if err := f.queries.AddUserScope(t.Context(), self); err != nil {
				t.Fatal(err)
			}
			if err := deleteScopeEntity(t.Context(), f.queries, scopes[index]); err != nil {
				t.Fatal(err)
			}
			for target, scope := range scopes {
				want := 0
				if target < index {
					want = 1
				}
				if got := scopeCount(t, f, scope); got != want {
					t.Errorf("%s scopes after deleting %s=%d, want %d", scope.EntityType, kind, got, want)
				}
			}
			if got := scopeCount(t, f, self); got != 1 {
				t.Errorf("self scopes=%d, want 1", got)
			}
		})
	}
}

func TestHierarchyScopeRequiresExistingEntity(t *testing.T) {
	for index, kind := range []genDb.EntityType{
		genDb.EntityTypeOrganization,
		genDb.EntityTypeWorkspace,
		genDb.EntityTypeResource,
	} {
		t.Run(string(kind), func(t *testing.T) {
			f := newDeployFixture(t)
			scope := hierarchyScopes(t, f)[index]
			scope.EntityID = uuid.New()
			err := f.queries.AddUserScope(t.Context(), scope)
			var postgresError *pgconn.PgError
			if !errors.As(err, &postgresError) || postgresError.Code != "23503" {
				t.Fatalf("grant missing %s=%v, want foreign key violation", kind, err)
			}
		})
	}
}

func waitForScopeReferenceLock(t *testing.T, f *deployFixture, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(lockTimeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("operation completed before the other transaction committed: %v", err)
		default:
		}
		var blocked bool
		err := f.pool.QueryRow(
			t.Context(),
			"SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock')",
		).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		time.Sleep(lockPollInterval)
	}
	t.Fatal("operation never waited on the entity reference")
}

func TestHierarchyScopeGrantAndDeleteSerialize(t *testing.T) {
	for index, kind := range []genDb.EntityType{
		genDb.EntityTypeOrganization,
		genDb.EntityTypeWorkspace,
		genDb.EntityTypeResource,
	} {
		for _, first := range []string{"grant", rolloutDeleteOperation} {
			t.Run(string(kind)+"/"+first, func(t *testing.T) {
				f := newDeployFixture(t)
				scope := hierarchyScopes(t, f)[index]
				tx, err := f.pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(context.Background())
				qtx := genDb.New(tx)
				done := make(chan error, 1)
				if first == "grant" {
					if grantErr := qtx.AddUserScope(t.Context(), scope); grantErr != nil {
						t.Fatal(grantErr)
					}
					go func() { done <- deleteScopeEntity(t.Context(), f.queries, scope) }()
				} else {
					if deleteErr := deleteScopeEntity(t.Context(), qtx, scope); deleteErr != nil {
						t.Fatal(deleteErr)
					}
					go func() { done <- f.queries.AddUserScope(t.Context(), scope) }()
				}
				waitForScopeReferenceLock(t, f, done)
				if commitErr := tx.Commit(t.Context()); commitErr != nil {
					t.Fatal(commitErr)
				}
				operationErr := <-done
				if first == rolloutDeleteOperation {
					var postgresError *pgconn.PgError
					if !errors.As(operationErr, &postgresError) || postgresError.Code != "23503" {
						t.Fatalf("grant after deletion=%v, want foreign key violation", operationErr)
					}
				} else if operationErr != nil {
					t.Fatal(operationErr)
				}
				if got := scopeCount(t, f, scope); got != 0 {
					t.Fatalf("remaining scopes=%d, want 0", got)
				}
			})
		}
	}
}

func TestOrgMembershipRejectsDeletedOrganization(t *testing.T) {
	f := newEventFixture(t)
	org, err := f.queries.GetOrgByID(t.Context(), uuid.MustParse(f.orgID))
	if err != nil {
		t.Fatal(err)
	}
	server := NewOrgServer(f.pool, f.queries, f.machine)
	if _, deleteErr := server.DeleteOrg(f.owner, &orgv1.DeleteOrgRequest{OrgId: f.orgID}); deleteErr != nil {
		t.Fatal(deleteErr)
	}
	_, err = server.AddOrgMember(f.owner, &orgv1.AddOrgMemberRequest{OrgId: f.orgID, UserId: org.CreatedBy.String()})
	wantCode(t, err, connect.CodeNotFound)
	var connectError *connect.Error
	if !errors.As(err, &connectError) || connectError.Message() != ErrOrgNotFound.Error() {
		t.Fatalf("add member after organization deletion=%v, want organization not found", err)
	}
}

func TestWorkspaceMembershipRejectsDeletedWorkspace(t *testing.T) {
	f := newEventFixture(t)
	org, err := f.queries.GetOrgByID(t.Context(), uuid.MustParse(f.orgID))
	if err != nil {
		t.Fatal(err)
	}
	server := NewWorkspaceServer(f.pool, f.queries, f.machine)
	created, err := server.CreateWorkspace(
		f.owner,
		&workspacev1.CreateWorkspaceRequest{OrgId: f.orgID, Name: "workspace"},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := scoped(t, f.queries, org.CreatedBy)
	if _, deleteErr := server.DeleteWorkspace(
		ctx,
		&workspacev1.DeleteWorkspaceRequest{WorkspaceId: created.GetWorkspaceId()},
	); deleteErr != nil {
		t.Fatal(deleteErr)
	}
	_, err = server.CreateMember(ctx, &workspacev1.CreateMemberRequest{
		WorkspaceId: created.GetWorkspaceId(), UserId: org.CreatedBy.String(), Scopes: []string{genDb.ScopeRead},
	})
	wantCode(t, err, connect.CodeNotFound)
}
