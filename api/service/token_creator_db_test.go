package service

import (
	"testing"

	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
)

func TestTokenMetadataIncludesCreator(t *testing.T) {
	f := newSecretFixture(t)
	server := NewTokenServer(f.pool, f.queries, f.machine)
	for _, tc := range []struct {
		name       string
		entityType tokenv1.EntityType
		entityID   string
	}{
		{"organization", tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION, f.orgID.String()},
		{"workspace", tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, f.workspaceID.String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const name = "creator-check"
			created, err := server.CreateToken(f.owner, &tokenv1.CreateTokenRequest{
				Name: name, EntityType: tc.entityType, EntityId: tc.entityID, ExpiresInSec: 60,
				Scopes: []*tokenv1.EntityScope{{
					Scope: tokenv1.Scope_SCOPE_READ, EntityType: tc.entityType, EntityId: tc.entityID,
				}},
			})
			if err != nil {
				t.Fatalf("create token: %v", err)
			}
			creator := f.ownerID.String()
			if got := created.GetTokenMetadata().GetCreatedBy(); got != creator {
				t.Fatalf("create token creator = %q, want %q", got, creator)
			}

			token, err := server.GetToken(f.owner, &tokenv1.GetTokenRequest{
				Name: name, EntityType: tc.entityType, EntityId: tc.entityID,
			})
			if err != nil {
				t.Fatalf("get token: %v", err)
			}
			if got := token.GetToken().GetCreatedBy(); got != creator {
				t.Fatalf("get token creator = %q, want %q", got, creator)
			}

			listed, err := server.ListTokens(f.owner, &tokenv1.ListTokensRequest{
				EntityType: tc.entityType, EntityId: tc.entityID,
			})
			if err != nil {
				t.Fatalf("list tokens: %v", err)
			}
			if tokens := listed.GetTokens(); len(tokens) != 1 || tokens[0].GetCreatedBy() != creator {
				t.Fatalf("list tokens = %v, want one token created by %q", tokens, creator)
			}
		})
	}
}
