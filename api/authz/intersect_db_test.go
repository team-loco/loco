package authz_test

import (
	"reflect"
	"testing"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/db/dbtest"
	queries "github.com/team-loco/loco/api/gen/db"
)

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
