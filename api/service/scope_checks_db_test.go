package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/authz"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/tvm"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

type scopeOwnerQueries struct {
	genDb.Querier
	workspaceCalls int
	resourceCalls  int
}

func (q *scopeOwnerQueries) GetOrganizationIDByWorkspaceID(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	q.workspaceCalls++
	return q.Querier.GetOrganizationIDByWorkspaceID(ctx, id)
}

func (q *scopeOwnerQueries) GetWorkspaceOrganizationIDByResourceID(
	ctx context.Context,
	id uuid.UUID,
) (genDb.GetWorkspaceOrganizationIDByResourceIDRow, error) {
	q.resourceCalls++
	return q.Querier.GetWorkspaceOrganizationIDByResourceID(ctx, id)
}

func TestIssueSharesScopeOwners(t *testing.T) {
	f := newSecretFixture(t)
	q := &scopeOwnerQueries{Querier: f.queries}
	machine := tvm.NewVendingMachine(f.pool, q, tvm.Config{MaxAPITokenDuration: time.Hour})
	scopes := make([]genDb.EntityScope, 0, 3)
	for _, scope := range []genDb.Scope{genDb.ScopeRead, genDb.ScopeWrite, genDb.ScopeAdmin} {
		scopes = append(
			scopes,
			genDb.EntityScope{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: scope},
		)
	}
	_, row, err := machine.Issue(t.Context(), "scope-owners", f.ownerID,
		genDb.Entity{Type: genDb.EntityTypeResource, ID: f.resourceID}, scopes, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(row.Scopes) != len(scopes) {
		t.Fatalf("stored scopes = %v", row.Scopes)
	}
	if q.resourceCalls != 1 {
		t.Fatalf("resource owner queries = %d, want 1", q.resourceCalls)
	}
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
	scopes, err := authz.New(f.pool, f.queries).UserScopes(t.Context(), f.ownerID)
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
