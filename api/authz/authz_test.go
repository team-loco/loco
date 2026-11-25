package authz_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/authztest"
	"github.com/team-loco/loco/api/db/dbtest"
	queries "github.com/team-loco/loco/api/gen/db"
)

type fakeQueries struct {
	queries.Querier
}

var (
	org2UUID = uuid.MustParse("01890000-0000-0000-0000-000000000012")
	ws2UUID  = uuid.MustParse("01890000-0000-0000-0000-000000000022")
	res1UUID = uuid.MustParse("01890000-0000-0000-0000-000000000031")
	res2UUID = uuid.MustParse("01890000-0000-0000-0000-000000000032")
	res3UUID = uuid.MustParse("01890000-0000-0000-0000-000000000033")
)

func (*fakeQueries) GetUserScopes(_ context.Context, userID uuid.UUID) ([]queries.GetUserScopesRow, error) {
	return authztest.UserScopes(userID)
}

func (*fakeQueries) GetOrganizationIDByWorkspaceID(_ context.Context, id uuid.UUID) (uuid.UUID, error) {
	if id == authztest.Workspace1 || id == ws2UUID {
		return authztest.Org1, nil
	}
	if id == authztest.Workspace3 {
		return org2UUID, nil
	}
	return uuid.UUID{}, pgx.ErrNoRows
}

func (*fakeQueries) GetWorkspaceOrganizationIDByResourceID(
	_ context.Context,
	id uuid.UUID,
) (queries.GetWorkspaceOrganizationIDByResourceIDRow, error) {
	if id == res1UUID {
		return queries.GetWorkspaceOrganizationIDByResourceIDRow{
			WorkspaceID: authztest.Workspace1,
			OrgID:       authztest.Org1,
		}, nil
	}
	if id == res2UUID {
		return queries.GetWorkspaceOrganizationIDByResourceIDRow{
			WorkspaceID: ws2UUID,
			OrgID:       authztest.Org1,
		}, nil
	}
	if id == res3UUID {
		return queries.GetWorkspaceOrganizationIDByResourceIDRow{
			WorkspaceID: authztest.Workspace3,
			OrgID:       org2UUID,
		}, nil
	}
	return queries.GetWorkspaceOrganizationIDByResourceIDRow{}, pgx.ErrNoRows
}

func grantedScopes(t *testing.T, a *authz.Authorizer, userID uuid.UUID) []queries.EntityScope {
	t.Helper()
	scopes, err := a.UserScopes(t.Context(), userID)
	if err != nil {
		t.Fatalf("user scopes: %v", err)
	}
	return scopes
}

// user 1 has only self read/write/admin
func TestUser1Permissions(t *testing.T) {
	a := authz.New(&fakeQueries{})
	granted := grantedScopes(t, a, authztest.User1)

	t.Run("denied org 1 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   authztest.Org1,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied workspace 1 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   authztest.Workspace1,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("granted self read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeUser,
			EntityID:   authztest.User1,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for self read, got: %v", err)
		}
	})

	t.Run("denied other user read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeUser,
			EntityID:   authztest.User2,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})
}

// user 2 has org 1 r, w, a
func TestUser2Permissions(t *testing.T) {
	a := authz.New(&fakeQueries{})
	granted := grantedScopes(t, a, authztest.User2)

	t.Run("granted org 1 admin", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   authztest.Org1,
			Scope:      queries.ScopeAdmin,
		})
		if err != nil {
			t.Errorf("expected no error for org 1 admin, got: %v", err)
		}
	})

	t.Run("granted org 1 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   authztest.Org1,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for org 1 read, got: %v", err)
		}
	})

	t.Run("granted workspace 2 write via org 1", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws2UUID,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 2 write via org 1, got: %v", err)
		}
	})

	t.Run("denied workspace 3 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   authztest.Workspace3,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 2 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org2UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("granted resource 2 write via org 1", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res2UUID,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for resource 2 write via org 1, got: %v", err)
		}
	})

	t.Run("denied resource 3 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res3UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})
}

// user 3 has org 1 r, w
func TestUser3Permissions(t *testing.T) {
	a := authz.New(&fakeQueries{})
	granted := grantedScopes(t, a, authztest.User3)

	t.Run("granted org 1 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   authztest.Org1,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for org 1 read, got: %v", err)
		}
	})

	t.Run("granted org 1 write", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   authztest.Org1,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for org 1 write, got: %v", err)
		}
	})

	t.Run("denied org 1 admin", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   authztest.Org1,
			Scope:      queries.ScopeAdmin,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error for org 1 admin, got: %v", err)
		}
	})

	t.Run("granted workspace 1 write via org 1", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   authztest.Workspace1,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 1 write via org 1, got: %v", err)
		}
	})

	t.Run("granted workspace 2 read via org 1", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws2UUID,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 2 read via org 1, got: %v", err)
		}
	})

	t.Run("denied workspace 3 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   authztest.Workspace3,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("granted resource 1 write via org 1", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res1UUID,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for resource 1 write via org 1, got: %v", err)
		}
	})

	t.Run("denied resource 3 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res3UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 2 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org2UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})
}

// user 4 has r of ws 1
func TestUser4Permissions(t *testing.T) {
	a := authz.New(&fakeQueries{})
	granted := grantedScopes(t, a, authztest.User4)

	t.Run("granted workspace 1 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   authztest.Workspace1,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 1 read, got: %v", err)
		}
	})

	t.Run("denied workspace 1 write", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   authztest.Workspace1,
			Scope:      queries.ScopeWrite,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error for workspace 1 write, got: %v", err)
		}
	})

	t.Run("denied workspace 1 admin", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   authztest.Workspace1,
			Scope:      queries.ScopeAdmin,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error for workspace 1 admin, got: %v", err)
		}
	})

	t.Run("denied workspace 2 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws2UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 1 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   authztest.Org1,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("granted resource 1 read via workspace 1", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res1UUID,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for resource 1 read via workspace 1, got: %v", err)
		}
	})

	t.Run("denied resource 1 write", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res1UUID,
			Scope:      queries.ScopeWrite,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error for resource 1 write, got: %v", err)
		}
	})

	t.Run("denied resource 2 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res2UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})
}

// user 5 has r, w, a of wks 3
func TestUser5Permissions(t *testing.T) {
	a := authz.New(&fakeQueries{})
	granted := grantedScopes(t, a, authztest.User5)

	t.Run("granted workspace 3 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   authztest.Workspace3,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 3 read, got: %v", err)
		}
	})

	t.Run("granted workspace 3 write", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   authztest.Workspace3,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 3 write, got: %v", err)
		}
	})

	t.Run("granted workspace 3 admin", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   authztest.Workspace3,
			Scope:      queries.ScopeAdmin,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 3 admin, got: %v", err)
		}
	})

	t.Run("denied workspace 1 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   authztest.Workspace1,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 1 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   authztest.Org1,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 2 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org2UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 2 admin", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org2UUID,
			Scope:      queries.ScopeAdmin,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 1 admin", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   authztest.Org1,
			Scope:      queries.ScopeAdmin,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("granted resource 3 read via workspace 3", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res3UUID,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for resource 3 read via workspace 3, got: %v", err)
		}
	})

	t.Run("granted resource 3 write via workspace 3", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res3UUID,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for resource 3 write via workspace 3, got: %v", err)
		}
	})

	t.Run("granted resource 3 admin via workspace 3", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res3UUID,
			Scope:      queries.ScopeAdmin,
		})
		if err != nil {
			t.Errorf("expected no error for resource 3 admin via workspace 3, got: %v", err)
		}
	})

	t.Run("denied resource 1 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res1UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})
}

func TestOrgMemberScopeDoesNotCascade(t *testing.T) {
	a := authz.New(&fakeQueries{})
	granted := []queries.EntityScope{
		{EntityType: queries.EntityTypeOrganization, EntityID: authztest.Org1, Scope: queries.ScopeMember},
	}
	denied := []queries.EntityScope{
		{EntityType: queries.EntityTypeOrganization, EntityID: authztest.Org1, Scope: queries.ScopeRead},
		{EntityType: queries.EntityTypeOrganization, EntityID: org2UUID, Scope: queries.ScopeMember},
		{EntityType: queries.EntityTypeWorkspace, EntityID: authztest.Workspace1, Scope: queries.ScopeRead},
		{EntityType: queries.EntityTypeResource, EntityID: res1UUID, Scope: queries.ScopeRead},
	}
	for _, required := range denied {
		if err := a.Check(t.Context(), granted, required); !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("org member got %+v: %v", required, err)
		}
	}
	member := queries.EntityScope{
		EntityType: queries.EntityTypeOrganization,
		EntityID:   authztest.Org1,
		Scope:      queries.ScopeMember,
	}
	if err := a.Check(t.Context(), granted, member); err != nil {
		t.Errorf("org member denied membership: %v", err)
	}
}

func TestEveryOrgScopeImpliesMembership(t *testing.T) {
	a := authz.New(&fakeQueries{})
	member := queries.EntityScope{
		EntityType: queries.EntityTypeOrganization,
		EntityID:   authztest.Org1,
		Scope:      queries.ScopeMember,
	}
	for _, scope := range []queries.Scope{queries.ScopeRead, queries.ScopeWrite, queries.ScopeAdmin} {
		granted := []queries.EntityScope{
			{EntityType: queries.EntityTypeOrganization, EntityID: authztest.Org1, Scope: scope},
		}
		if err := a.Check(t.Context(), granted, member); err != nil {
			t.Errorf("org %s denied membership: %v", scope, err)
		}
	}
	system := []queries.EntityScope{
		{EntityType: queries.EntityTypeSystem, EntityID: uuid.Nil, Scope: queries.ScopeRead},
	}
	if err := a.Check(t.Context(), system, member); err != nil {
		t.Errorf("system read denied membership: %v", err)
	}
}

func TestSystemRolesCoverEveryEntity(t *testing.T) {
	a := authz.New(&fakeQueries{})
	entities := []queries.Entity{
		{Type: queries.EntityTypeSystem},
		{Type: queries.EntityTypeOrganization, ID: org2UUID},
		{Type: queries.EntityTypeWorkspace, ID: authztest.Workspace1},
		{Type: queries.EntityTypeResource, ID: res1UUID},
		{Type: queries.EntityTypeUser, ID: authztest.User1},
	}
	held := map[queries.Scope][]queries.Scope{
		queries.ScopeRead:  {queries.ScopeMember, queries.ScopeRead},
		queries.ScopeAdmin: {queries.ScopeMember, queries.ScopeRead, queries.ScopeWrite, queries.ScopeAdmin},
	}
	for role, allowed := range held {
		granted := []queries.EntityScope{{EntityType: queries.EntityTypeSystem, Scope: role}}
		for _, entity := range entities {
			for _, scope := range []queries.Scope{
				queries.ScopeMember, queries.ScopeRead, queries.ScopeWrite, queries.ScopeAdmin,
			} {
				required := queries.EntityScope{EntityType: entity.Type, EntityID: entity.ID, Scope: scope}
				err := a.Check(t.Context(), granted, required)
				if slices.Contains(allowed, scope) != (err == nil) {
					t.Errorf("system %s on %s %s: %v", role, entity.Type, scope, err)
				}
			}
		}
	}
}

func TestCheckReportsDatabaseFailure(t *testing.T) {
	pool := dbtest.ClosedPool(t)
	a := authz.New(queries.New(pool))
	for _, entityType := range []queries.EntityType{queries.EntityTypeWorkspace, queries.EntityTypeResource} {
		required := queries.EntityScope{EntityType: entityType, EntityID: uuid.New(), Scope: queries.ScopeRead}
		err := a.Check(t.Context(), nil, required)
		if err == nil || errors.Is(err, authz.ErrEntityNotFound) || errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("%s lookup on a closed pool: got %v, want the database error", entityType, err)
		}
	}
}

func TestReadableEntities(t *testing.T) {
	a := authz.New(nil)
	granted := []queries.EntityScope{
		{EntityType: queries.EntityTypeOrganization, EntityID: authztest.Org1, Scope: queries.ScopeRead},
		{EntityType: queries.EntityTypeOrganization, EntityID: org2UUID, Scope: queries.ScopeAdmin},
		{EntityType: queries.EntityTypeWorkspace, EntityID: authztest.Workspace1, Scope: queries.ScopeRead},
		{EntityType: queries.EntityTypeWorkspace, EntityID: authztest.Workspace3, Scope: queries.ScopeWrite},
		{EntityType: queries.EntityTypeResource, EntityID: res1UUID, Scope: queries.ScopeRead},
		{EntityType: queries.EntityTypeResource, EntityID: uuid.New(), Scope: queries.ScopeMember},
		{EntityType: queries.EntityTypeUser, EntityID: authztest.User1, Scope: queries.ScopeRead},
	}
	got := a.ReadableEntities(granted)
	if got.System || !slices.Equal(got.Organizations, []uuid.UUID{authztest.Org1}) ||
		!slices.Equal(got.Workspaces, []uuid.UUID{authztest.Workspace1}) ||
		!slices.Equal(got.Resources, []uuid.UUID{res1UUID}) {
		t.Fatalf("readable entities = %+v", got)
	}
	for _, scope := range []queries.Scope{queries.ScopeRead, queries.ScopeWrite, queries.ScopeAdmin} {
		entities := a.ReadableEntities([]queries.EntityScope{{EntityType: queries.EntityTypeSystem, Scope: scope}})
		if entities.System != (scope != queries.ScopeWrite) {
			t.Fatalf("system %s: %+v", scope, entities)
		}
	}
	empty := a.ReadableEntities(nil)
	if empty.System || len(empty.Organizations)+len(empty.Workspaces)+len(empty.Resources) != 0 {
		t.Fatalf("empty grants expose entities: %+v", empty)
	}
}
