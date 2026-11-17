package authz_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/db/dbtest"
	queries "github.com/team-loco/loco/api/gen/db"
)

func TestIntersectPreservesMembershipBetweenSystemPermissions(t *testing.T) {
	t.Parallel()
	authorizer := authz.New(nil)
	member := queries.EntityScope{
		EntityType: queries.EntityTypeOrganization,
		EntityID:   org1UUID,
		Scope:      queries.ScopeMember,
	}
	for _, permissions := range [][2]queries.Scope{
		{queries.ScopeRead, queries.ScopeAdmin},
		{queries.ScopeAdmin, queries.ScopeRead},
	} {
		left := []queries.EntityScope{{EntityType: queries.EntityTypeSystem, Scope: permissions[0]}}
		right := []queries.EntityScope{{EntityType: queries.EntityTypeSystem, Scope: permissions[1]}}
		for _, grants := range [][]queries.EntityScope{left, right} {
			if err := authorizer.Check(t.Context(), grants, member); err != nil {
				t.Fatalf("original system grant does not cover organization membership: %v", err)
			}
		}
		common, err := authorizer.Intersect(t.Context(), left, right)
		if err != nil {
			t.Fatal(err)
		}
		if err := authorizer.Check(t.Context(), common, member); err != nil {
			t.Fatalf("intersection lost shared organization membership: %v", err)
		}
		for _, permission := range []queries.Scope{queries.ScopeRead, queries.ScopeWrite, queries.ScopeAdmin} {
			wanted := member
			wanted.Scope = permission
			if err := authorizer.Check(t.Context(), common, wanted); !errors.Is(err, authz.ErrInsufficientPermissions) {
				t.Fatalf("intersection permits organization %s: %v", permission, err)
			}
		}
	}
}

func TestIntersectNarrowsToSharedHierarchyAndPermission(t *testing.T) {
	t.Parallel()
	pool := dbtest.NewPool(t)
	q := queries.New(pool)
	ctx := t.Context()
	user, err := q.CreateUser(ctx, queries.CreateUserParams{Email: "intersection@tokens.test"})
	if err != nil {
		t.Fatal(err)
	}
	org, err := q.CreateOrganization(ctx, queries.CreateOrganizationParams{Name: "intersection", CreatedBy: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	other, err := q.CreateOrganization(ctx, queries.CreateOrganizationParams{Name: "other", CreatedBy: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := q.CreateWorkspace(
		ctx,
		queries.CreateWorkspaceParams{OrgID: org.ID, Name: "target", CreatedBy: user.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	orgWrite := queries.EntityScope{
		EntityType: queries.EntityTypeOrganization,
		EntityID:   org.ID,
		Scope:      queries.ScopeWrite,
	}
	orgMember := queries.EntityScope{
		EntityType: queries.EntityTypeOrganization,
		EntityID:   org.ID,
		Scope:      queries.ScopeMember,
	}
	orgRead := queries.EntityScope{
		EntityType: queries.EntityTypeOrganization,
		EntityID:   org.ID,
		Scope:      queries.ScopeRead,
	}
	workspaceRead := queries.EntityScope{
		EntityType: queries.EntityTypeWorkspace,
		EntityID:   workspace,
		Scope:      queries.ScopeRead,
	}
	workspaceWrite := queries.EntityScope{
		EntityType: queries.EntityTypeWorkspace,
		EntityID:   workspace,
		Scope:      queries.ScopeWrite,
	}
	otherRead := queries.EntityScope{
		EntityType: queries.EntityTypeOrganization,
		EntityID:   other.ID,
		Scope:      queries.ScopeRead,
	}
	for _, tc := range []struct {
		name                string
		ceiling, live, want []queries.EntityScope
	}{
		{name: "stored parent live child", ceiling: []queries.EntityScope{orgRead},
			live: []queries.EntityScope{workspaceRead}, want: []queries.EntityScope{workspaceRead}},
		{name: "stored child live parent", ceiling: []queries.EntityScope{workspaceRead},
			live: []queries.EntityScope{orgRead}, want: []queries.EntityScope{workspaceRead}},
		{name: "different org permissions retain membership", ceiling: []queries.EntityScope{orgRead},
			live: []queries.EntityScope{orgWrite}, want: []queries.EntityScope{orgMember}},
		{name: "inverse org permissions retain membership", ceiling: []queries.EntityScope{orgWrite},
			live: []queries.EntityScope{orgRead}, want: []queries.EntityScope{orgMember}},
		{name: "different permission", ceiling: []queries.EntityScope{orgRead}, live: []queries.EntityScope{workspaceWrite}},
		{name: "another organization", ceiling: []queries.EntityScope{orgRead}, live: []queries.EntityScope{otherRead}},
		{name: "no live grants", ceiling: []queries.EntityScope{orgRead}},
		{name: "duplicate grant", ceiling: []queries.EntityScope{orgRead},
			live: []queries.EntityScope{workspaceRead, workspaceRead}, want: []queries.EntityScope{workspaceRead}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := authz.New(q).Intersect(ctx, tc.ceiling, tc.live)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("intersection = %v, want %v", got, tc.want)
			}
		})
	}
}
