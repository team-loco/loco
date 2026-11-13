package service

import (
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/tvm"

	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
)

func TestTokenMetadataIncludesCreator(t *testing.T) {
	f := newSecretFixture(t)
	server := NewTokenServer(f.pool, f.queries, f.machine, limits.Defaults())
	for _, tc := range []struct {
		entityType tokenv1.EntityType
		entityID   string
	}{
		{tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION, f.orgID.String()},
		{tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, f.workspaceID.String()},
	} {
		t.Run(tc.entityType.String(), func(t *testing.T) {
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

func TestCreateTokenRespectsConfiguredMaximum(t *testing.T) {
	f := newSecretFixture(t)
	machine := tvm.NewVendingMachine(
		f.pool,
		f.queries,
		tvm.Config{MaxAPITokenDuration: 90 * time.Second, LastUsedUpdateInterval: time.Minute},
	)
	server := NewTokenServer(f.pool, f.queries, machine, limits.Defaults())
	for _, seconds := range []int64{0, -1, 91, 90} {
		_, err := server.CreateToken(f.owner, &tokenv1.CreateTokenRequest{
			Name:         "lifetime-check",
			EntityType:   tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
			EntityId:     f.workspaceID.String(),
			ExpiresInSec: seconds,
			Scopes: []*tokenv1.EntityScope{
				{
					Scope:      tokenv1.Scope_SCOPE_READ,
					EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
					EntityId:   f.workspaceID.String(),
				},
			},
		})
		if seconds != 90 {
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("expiry %d: %v", seconds, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCreateTokenStopsAtTheOwnersLimit(t *testing.T) {
	f := newSecretFixture(t)
	counts := limits.Defaults()
	limits.TokensPerEntity.Set(counts, 1)
	server := NewTokenServer(f.pool, f.queries, f.machine, counts)
	create := func(entityType tokenv1.EntityType, entityID, name string) error {
		_, err := server.CreateToken(f.owner, &tokenv1.CreateTokenRequest{
			Name:         name,
			EntityType:   entityType,
			EntityId:     entityID,
			ExpiresInSec: 60,
			Scopes: []*tokenv1.EntityScope{
				{Scope: tokenv1.Scope_SCOPE_READ, EntityType: entityType, EntityId: entityID},
			},
		})
		return err
	}
	workspace := tokenv1.EntityType_ENTITY_TYPE_WORKSPACE
	if err := create(workspace, f.workspaceID.String(), "first"); err != nil {
		t.Fatalf("first token: %v", err)
	}
	wantCode(t, create(workspace, f.workspaceID.String(), "second"), connect.CodeResourceExhausted)
	if err := create(tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION, f.orgID.String(), "first"); err != nil {
		t.Fatalf("another owner hit the workspace's limit: %v", err)
	}
}
