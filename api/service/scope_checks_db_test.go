package service

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/authz"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

type scopeOwnerQueries struct {
	genDb.Querier
	workspaceCalls int
}

func (q *scopeOwnerQueries) GetOrganizationIDByWorkspaceID(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	q.workspaceCalls++
	return q.Querier.GetOrganizationIDByWorkspaceID(ctx, id)
}

func TestCreateMemberSharesScopeOwners(t *testing.T) {
	f := newSecretFixture(t)
	q := &scopeOwnerQueries{Querier: f.queries}
	server := NewWorkspaceServer(f.pool, q, limits.Defaults())
	member := signUp(t, f.resolver, "member", "member@secrets.test")
	membership := genDb.AddUserScopeParams{
		UserID: member, EntityType: genDb.EntityTypeOrganization, EntityID: f.orgID, Scope: genDb.ScopeMember,
	}
	if err := f.queries.AddUserScope(t.Context(), membership); err != nil {
		t.Fatal(err)
	}
	// The owner's grants without its direct workspace ones, so access resolves through the organization.
	owner, _ := reqctx.PrincipalFrom(f.owner)
	owner.Scopes = slices.DeleteFunc(slices.Clone(owner.Scopes), func(scope genDb.EntityScope) bool {
		return scope.EntityType == genDb.EntityTypeWorkspace
	})
	orgAdmin := reqctx.WithPrincipal(t.Context(), owner)
	if _, err := server.CreateMember(orgAdmin, &workspacev1.CreateMemberRequest{
		WorkspaceId: f.workspaceID.String(),
		UserId:      member.String(),
		Scopes:      []string{genDb.ScopeRead, genDb.ScopeWrite, genDb.ScopeAdmin},
	}); err != nil {
		t.Fatal(err)
	}
	if q.workspaceCalls != 1 {
		t.Fatalf("authorization owner queries = %d, want 1", q.workspaceCalls)
	}
	scopes, err := authz.New(f.queries).UserScopes(t.Context(), member)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []genDb.Scope{genDb.ScopeRead, genDb.ScopeWrite, genDb.ScopeAdmin} {
		required := genDb.EntityScope{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID, Scope: scope}
		found := false
		for _, granted := range scopes {
			if granted == required {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing workspace scope %s", scope)
		}
	}
}
