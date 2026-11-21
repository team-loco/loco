package service

import (
	"context"
	"slices"
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
)

func TestListTokensNamesTheScopesTheCallerCanRead(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	server := NewTokenServer(f.pool, f.queries, f.machine, limits.Defaults())
	read := func(entityType tokenv1.EntityType, id string) *tokenv1.EntityScope {
		return &tokenv1.EntityScope{Scope: tokenv1.Scope_SCOPE_READ, EntityType: entityType, EntityId: id}
	}
	org := read(tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION, f.orgID.String())
	workspace := read(tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, f.workspaceID.String())
	resource := read(tokenv1.EntityType_ENTITY_TYPE_RESOURCE, f.resourceID.String())
	create := func(owner *tokenv1.EntityScope, scopes ...*tokenv1.EntityScope) {
		t.Helper()
		if _, err := server.CreateToken(f.owner, &tokenv1.CreateTokenRequest{
			Name: "names", EntityType: owner.GetEntityType(), EntityId: owner.GetEntityId(),
			ExpiresInSec: 60, Scopes: scopes,
		}); err != nil {
			t.Fatalf("create token: %v", err)
		}
	}
	names := func(ctx context.Context, owner *tokenv1.EntityScope) []string {
		t.Helper()
		listed, err := server.ListTokens(ctx, &tokenv1.ListTokensRequest{
			EntityType: owner.GetEntityType(), EntityId: owner.GetEntityId(),
		})
		if err != nil {
			t.Fatalf("list tokens: %v", err)
		}
		if len(listed.GetTokens()) != 1 {
			t.Fatalf("tokens = %v, want one", listed.GetTokens())
		}
		scopes := listed.GetTokens()[0].GetScopes()
		got := make([]string, len(scopes))
		for i, scope := range scopes {
			got[i] = scope.GetName()
		}
		return got
	}

	create(org, org, workspace, resource)
	if got, want := names(f.owner, org), []string{"secrets", "storefront", webService}; !slices.Equal(got, want) {
		t.Fatalf("organization token scope names = %q, want %q", got, want)
	}

	personal := read(tokenv1.EntityType_ENTITY_TYPE_USER, f.ownerID.String())
	create(personal, workspace, resource)
	resourceReader := reqctx.WithPrincipal(t.Context(), reqctx.Principal{
		Entity: genDb.Entity{Type: genDb.EntityTypeUser, ID: f.ownerID},
		Scopes: []genDb.EntityScope{
			{EntityType: genDb.EntityTypeUser, EntityID: f.ownerID, Scope: genDb.ScopeRead},
			{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeRead},
		},
		UserID: f.ownerID,
	})
	if got, want := names(resourceReader, personal), []string{"", webService}; !slices.Equal(got, want) {
		t.Fatalf("personal token scope names for a resource reader = %q, want %q", got, want)
	}
}
