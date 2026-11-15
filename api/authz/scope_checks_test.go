package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/authz"
	queries "github.com/team-loco/loco/api/gen/db"
)

type countedOwners struct {
	fakeQueries
	calls int
}

func (q *countedOwners) GetOrganizationIDByWorkspaceID(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	q.calls++
	return q.fakeQueries.GetOrganizationIDByWorkspaceID(ctx, id)
}

func (q *countedOwners) GetWorkspaceOrganizationIDByResourceID(
	ctx context.Context,
	id uuid.UUID,
) (queries.GetWorkspaceOrganizationIDByResourceIDRow, error) {
	q.calls++
	return q.fakeQueries.GetWorkspaceOrganizationIDByResourceID(ctx, id)
}

func TestScopeChecksShareOwners(t *testing.T) {
	for _, entity := range []queries.Entity{
		{Type: queries.EntityTypeWorkspace, ID: ws1UUID},
		{Type: queries.EntityTypeResource, ID: res1UUID},
	} {
		t.Run(string(entity.Type), func(t *testing.T) {
			q := &countedOwners{}
			a := authz.New(nil, q)
			granted := grantedScopes(t, a, user2UUID)
			required := []queries.EntityScope{
				{EntityType: entity.Type, EntityID: entity.ID, Scope: queries.ScopeRead},
				{EntityType: entity.Type, EntityID: entity.ID, Scope: queries.ScopeWrite},
				{EntityType: entity.Type, EntityID: entity.ID, Scope: queries.ScopeAdmin},
			}
			if err := a.Check(t.Context(), granted, required...); err != nil {
				t.Fatal(err)
			}
			if q.calls != 1 {
				t.Fatalf("owner queries = %d, want 1", q.calls)
			}
			if err := a.Check(t.Context(), granted, required...); err != nil {
				t.Fatal(err)
			}
			if q.calls != 2 {
				t.Fatalf("owner queries across two checks = %d, want 2", q.calls)
			}
		})
	}
}

func TestScopeCheckDecisions(t *testing.T) {
	entities := []queries.Entity{
		{Type: queries.EntityTypeUser, ID: user1UUID},
		{Type: queries.EntityTypeOrganization, ID: org1UUID},
		{Type: queries.EntityTypeWorkspace, ID: ws1UUID},
		{Type: queries.EntityTypeWorkspace, ID: ws3UUID},
		{Type: queries.EntityTypeResource, ID: res1UUID},
		{Type: queries.EntityTypeResource, ID: res3UUID},
		{Type: queries.EntityTypeResource, ID: uuid.Nil},
		{Type: queries.EntityTypeSystem},
	}
	for _, user := range []uuid.UUID{user1UUID, user2UUID, user3UUID, user4UUID, user5UUID} {
		a := authz.New(nil, &fakeQueries{})
		granted := grantedScopes(t, a, user)
		for _, entity := range entities {
			scopes := []queries.Scope{queries.ScopeRead, queries.ScopeWrite, queries.ScopeAdmin, queries.ScopeMember}
			for _, first := range scopes {
				for _, second := range scopes {
					required := []queries.EntityScope{
						{EntityType: entity.Type, EntityID: entity.ID, Scope: first},
						{EntityType: entity.Type, EntityID: entity.ID, Scope: second},
					}
					var want error
					for _, scope := range required {
						if want = a.Check(t.Context(), granted, scope); want != nil {
							break
						}
					}
					if got := a.Check(t.Context(), granted, required...); !errors.Is(got, want) {
						t.Fatalf(
							"user %s entity %v scopes %s/%s: got %v, want %v",
							user,
							entity,
							first,
							second,
							got,
							want,
						)
					}
				}
			}
		}
	}
}

func TestScopeChecksDirectGrantsSkipOwners(t *testing.T) {
	required := []queries.EntityScope{
		{EntityType: queries.EntityTypeResource, EntityID: res1UUID, Scope: queries.ScopeRead},
		{EntityType: queries.EntityTypeWorkspace, EntityID: ws1UUID, Scope: queries.ScopeWrite},
	}
	for _, granted := range [][]queries.EntityScope{required, {
		{EntityType: queries.EntityTypeSystem, Scope: queries.ScopeRead},
		{EntityType: queries.EntityTypeSystem, Scope: queries.ScopeWrite},
	}} {
		q := &countedOwners{}
		if err := authz.New(nil, q).Check(t.Context(), granted, required...); err != nil {
			t.Fatal(err)
		}
		if q.calls != 0 {
			t.Fatalf("owner queries = %d, want 0", q.calls)
		}
	}
}
