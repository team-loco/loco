package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/authz"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
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
	if _, err := server.CreateMember(f.owner, &workspacev1.CreateMemberRequest{
		WorkspaceId: f.workspaceID.String(),
		UserId:      f.ownerID.String(),
		Scopes:      []string{genDb.ScopeRead, genDb.ScopeWrite, genDb.ScopeAdmin},
	}); err != nil {
		t.Fatal(err)
	}
	if q.workspaceCalls != 1 {
		t.Fatalf("authorization owner queries = %d, want 1", q.workspaceCalls)
	}
	scopes, err := authz.New(f.queries).UserScopes(t.Context(), f.ownerID)
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
