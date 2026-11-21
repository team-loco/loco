package service

import (
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/tvm"

	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
)

func TestTokenMetadataIncludesCreator(t *testing.T) {
	t.Parallel()
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

			listed, err := server.ListTokens(f.owner, &tokenv1.ListTokensRequest{
				EntityType: tc.entityType, EntityId: tc.entityID,
			})
			if err != nil {
				t.Fatalf("list tokens: %v", err)
			}
			tokens := listed.GetTokens()
			if len(tokens) != 1 || tokens[0].GetName() != name || tokens[0].GetCreatedBy() != creator {
				t.Fatalf("list tokens = %v, want token %q created by %q", tokens, name, creator)
			}
			for _, metadata := range append(listed.GetTokens(), created.GetTokenMetadata()) {
				secret := created.GetToken()
				if metadata.GetDisplaySuffix() != secret[len(secret)-4:] {
					t.Fatalf("display suffix = %v", metadata)
				}
				encoded, marshalErr := protojson.Marshal(metadata)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				if strings.Contains(string(encoded), secret) {
					t.Fatal("metadata contains the secret")
				}
			}
		})
	}
}

func TestCreateTokenRespectsConfiguredMaximum(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	machine := tvm.NewVendingMachine(f.queries,
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
	t.Parallel()
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
	if _, err := f.pool.Exec(t.Context(),
		"UPDATE api_tokens SET expires_at = NOW() - INTERVAL '1 second' WHERE entity_id = $1", f.workspaceID,
	); err != nil {
		t.Fatal(err)
	}
	if err := create(workspace, f.workspaceID.String(), "replacement"); err != nil {
		t.Fatalf("expired token occupied the owner's limit: %v", err)
	}
	wantCode(t, create(workspace, f.workspaceID.String(), "third"), connect.CodeResourceExhausted)
	if err := create(tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION, f.orgID.String(), "first"); err != nil {
		t.Fatalf("another owner hit the workspace's limit: %v", err)
	}
}

func TestCreateTokenReplacesAnExpiredTokenOfTheSameName(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	server := NewTokenServer(f.pool, f.queries, f.machine, limits.Defaults())
	request := &tokenv1.CreateTokenRequest{
		Name:         "deploy",
		EntityType:   tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		EntityId:     f.workspaceID.String(),
		ExpiresInSec: 60,
		Scopes: []*tokenv1.EntityScope{{
			Scope:      tokenv1.Scope_SCOPE_READ,
			EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
			EntityId:   f.workspaceID.String(),
		}},
	}
	if _, err := server.CreateToken(f.owner, request); err != nil {
		t.Fatal(err)
	}
	_, err := server.CreateToken(f.owner, request)
	wantCode(t, err, connect.CodeAlreadyExists)
	if _, err = f.pool.Exec(t.Context(),
		"UPDATE api_tokens SET expires_at = NOW() - INTERVAL '1 second' WHERE entity_id = $1", f.workspaceID,
	); err != nil {
		t.Fatal(err)
	}
	replacement, err := server.CreateToken(f.owner, request)
	if err != nil {
		t.Fatalf("an expired token kept its name: %v", err)
	}
	listed, err := server.ListTokens(f.owner, &tokenv1.ListTokensRequest{
		EntityType: request.GetEntityType(), EntityId: request.GetEntityId(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tokens := listed.GetTokens()
	if len(tokens) != 1 || tokens[0].GetDisplaySuffix() != replacement.GetTokenMetadata().GetDisplaySuffix() {
		t.Fatalf("tokens = %v, want only the replacement", tokens)
	}
}

func TestExpiredTokensAreKeptForTheirOrganizationsRetention(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	server := NewTokenServer(f.pool, f.queries, f.machine, limits.Defaults())
	create := func(entityType tokenv1.EntityType, entityID, name string, expiredDays int) {
		t.Helper()
		if _, err := server.CreateToken(f.owner, &tokenv1.CreateTokenRequest{
			Name:         name,
			EntityType:   entityType,
			EntityId:     entityID,
			ExpiresInSec: 60,
			Scopes: []*tokenv1.EntityScope{
				{Scope: tokenv1.Scope_SCOPE_READ, EntityType: entityType, EntityId: entityID},
			},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(t.Context(),
			"UPDATE api_tokens SET expires_at = NOW() - make_interval(days => $1) WHERE name = $2",
			expiredDays, name,
		); err != nil {
			t.Fatal(err)
		}
	}
	remaining := func() []string {
		t.Helper()
		rows, err := f.pool.Query(t.Context(), "SELECT name FROM api_tokens ORDER BY name")
		if err != nil {
			t.Fatal(err)
		}
		names, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		return names
	}
	organization, workspace := tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION, tokenv1.EntityType_ENTITY_TYPE_WORKSPACE
	create(organization, f.orgID.String(), "org-recent", 2)
	create(organization, f.orgID.String(), "org-old", 40)
	create(workspace, f.workspaceID.String(), "workspace-recent", 2)
	if err := f.queries.DeleteExpiredAPITokens(t.Context(), 30); err != nil {
		t.Fatal(err)
	}
	if got := remaining(); !slices.Equal(got, []string{"org-recent", "workspace-recent"}) {
		t.Fatalf("after the 30-day default: %v, want the tokens expired 2 days ago", got)
	}
	if _, err := f.queries.UpsertOrgLimitOverrides(t.Context(), genDb.UpsertOrgLimitOverridesParams{
		OrgID: f.orgID, ExpiredTokenRetentionDays: new(int32(1)), Reason: "shorter token retention",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.queries.DeleteExpiredAPITokens(t.Context(), 30); err != nil {
		t.Fatal(err)
	}
	if got := remaining(); len(got) != 0 {
		t.Fatalf("after a 1-day override: %v, want none", got)
	}
}
