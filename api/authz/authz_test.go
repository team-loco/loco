package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/team-loco/loco/api/authz"
	queries "github.com/team-loco/loco/api/gen/db"
)

type fakeQueries struct {
	queries.Querier
}

var (
	errUnknownUser   = errors.New("unknown test user")
	errUnknownEntity = errors.New("unknown test entity")
)

var (
	user1UUID = uuid.MustParse("01890000-0000-0000-0000-000000000001")
	user2UUID = uuid.MustParse("01890000-0000-0000-0000-000000000002")
	user3UUID = uuid.MustParse("01890000-0000-0000-0000-000000000003")
	user4UUID = uuid.MustParse("01890000-0000-0000-0000-000000000004")
	user5UUID = uuid.MustParse("01890000-0000-0000-0000-000000000005")
	org1UUID  = uuid.MustParse("01890000-0000-0000-0000-000000000011")
	org2UUID  = uuid.MustParse("01890000-0000-0000-0000-000000000012")
	ws1UUID   = uuid.MustParse("01890000-0000-0000-0000-000000000021")
	ws2UUID   = uuid.MustParse("01890000-0000-0000-0000-000000000022")
	ws3UUID   = uuid.MustParse("01890000-0000-0000-0000-000000000023")
	res1UUID  = uuid.MustParse("01890000-0000-0000-0000-000000000031")
	res2UUID  = uuid.MustParse("01890000-0000-0000-0000-000000000032")
	res3UUID  = uuid.MustParse("01890000-0000-0000-0000-000000000033")
)

func (*fakeQueries) GetUserScopes(_ context.Context, userID uuid.UUID) ([]queries.GetUserScopesRow, error) {
	switch userID {
	case user1UUID:
		return []queries.GetUserScopesRow{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: user1UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: user1UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: user1UUID},
		}, nil
	case user2UUID:
		return []queries.GetUserScopesRow{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: user2UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: user2UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: user2UUID},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeOrganization, EntityID: org1UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeOrganization, EntityID: org1UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeOrganization, EntityID: org1UUID},
		}, nil
	case user3UUID:
		return []queries.GetUserScopesRow{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: user3UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: user3UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: user3UUID},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeOrganization, EntityID: org1UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeOrganization, EntityID: org1UUID},
		}, nil
	case user4UUID:
		return []queries.GetUserScopesRow{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: user4UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: user4UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: user4UUID},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeWorkspace, EntityID: ws1UUID},
		}, nil
	case user5UUID:
		return []queries.GetUserScopesRow{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: user5UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: user5UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: user5UUID},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeWorkspace, EntityID: ws3UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeWorkspace, EntityID: ws3UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeWorkspace, EntityID: ws3UUID},
		}, nil
	default:
		return nil, errUnknownUser
	}
}

func (*fakeQueries) GetOrganizationIDByWorkspaceID(_ context.Context, id uuid.UUID) (uuid.UUID, error) {
	if id == ws1UUID || id == ws2UUID {
		return org1UUID, nil
	}
	if id == ws3UUID {
		return org2UUID, nil
	}
	return uuid.UUID{}, errUnknownEntity
}

func (*fakeQueries) GetWorkspaceOrganizationIDByResourceID(
	_ context.Context,
	id uuid.UUID,
) (queries.GetWorkspaceOrganizationIDByResourceIDRow, error) {
	if id == res1UUID {
		return queries.GetWorkspaceOrganizationIDByResourceIDRow{
			WorkspaceID: ws1UUID,
			OrgID:       org1UUID,
		}, nil
	}
	if id == res2UUID {
		return queries.GetWorkspaceOrganizationIDByResourceIDRow{
			WorkspaceID: ws2UUID,
			OrgID:       org1UUID,
		}, nil
	}
	if id == res3UUID {
		return queries.GetWorkspaceOrganizationIDByResourceIDRow{
			WorkspaceID: ws3UUID,
			OrgID:       org2UUID,
		}, nil
	}
	return queries.GetWorkspaceOrganizationIDByResourceIDRow{}, errUnknownEntity
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
	a := authz.New(nil, &fakeQueries{})
	granted := grantedScopes(t, a, user1UUID)

	t.Run("denied org 1 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org1UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied workspace 1 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws1UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("granted self read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeUser,
			EntityID:   user1UUID,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for self read, got: %v", err)
		}
	})

	t.Run("denied other user read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeUser,
			EntityID:   user2UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})
}

// user 2 has org 1 r, w, a
func TestUser2Permissions(t *testing.T) {
	a := authz.New(nil, &fakeQueries{})
	granted := grantedScopes(t, a, user2UUID)

	t.Run("granted org 1 admin", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org1UUID,
			Scope:      queries.ScopeAdmin,
		})
		if err != nil {
			t.Errorf("expected no error for org 1 admin, got: %v", err)
		}
	})

	t.Run("granted org 1 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org1UUID,
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
			EntityID:   ws3UUID,
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
	a := authz.New(nil, &fakeQueries{})
	granted := grantedScopes(t, a, user3UUID)

	t.Run("granted org 1 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org1UUID,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for org 1 read, got: %v", err)
		}
	})

	t.Run("granted org 1 write", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org1UUID,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for org 1 write, got: %v", err)
		}
	})

	t.Run("denied org 1 admin", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org1UUID,
			Scope:      queries.ScopeAdmin,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error for org 1 admin, got: %v", err)
		}
	})

	t.Run("granted workspace 1 write via org 1", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws1UUID,
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
			EntityID:   ws3UUID,
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
	a := authz.New(nil, &fakeQueries{})
	granted := grantedScopes(t, a, user4UUID)

	t.Run("granted workspace 1 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws1UUID,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 1 read, got: %v", err)
		}
	})

	t.Run("denied workspace 1 write", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws1UUID,
			Scope:      queries.ScopeWrite,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error for workspace 1 write, got: %v", err)
		}
	})

	t.Run("denied workspace 1 admin", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws1UUID,
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
			EntityID:   org1UUID,
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
	a := authz.New(nil, &fakeQueries{})
	granted := grantedScopes(t, a, user5UUID)

	t.Run("granted workspace 3 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws3UUID,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 3 read, got: %v", err)
		}
	})

	t.Run("granted workspace 3 write", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws3UUID,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 3 write, got: %v", err)
		}
	})

	t.Run("granted workspace 3 admin", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws3UUID,
			Scope:      queries.ScopeAdmin,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 3 admin, got: %v", err)
		}
	})

	t.Run("denied workspace 1 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws1UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, authz.ErrInsufficientPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 1 read", func(t *testing.T) {
		err := a.Check(t.Context(), granted, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org1UUID,
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
			EntityID:   org1UUID,
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
