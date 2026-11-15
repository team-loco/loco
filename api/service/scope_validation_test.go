package service

import (
	"testing"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestCreateTokenScopeValidation(t *testing.T) {
	for _, n := range []int{0, 1, 64, 65} {
		req := &tokenv1.CreateTokenRequest{
			Name: "scope-limit", EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
			EntityId: uuid.NewString(), ExpiresInSec: 60,
		}
		for range n {
			req.Scopes = append(req.Scopes, &tokenv1.EntityScope{
				EntityType: tokenv1.EntityType_ENTITY_TYPE_RESOURCE,
				EntityId:   uuid.NewString(),
				Scope:      tokenv1.Scope_SCOPE_READ,
			})
		}
		err := protovalidate.Validate(req)
		if valid := n > 0 && n <= 64; (err == nil) != valid {
			t.Fatalf("%d scopes: %v", n, err)
		}
	}
}

func TestCreateTokenScopeIdentity(t *testing.T) {
	id := uuid.NewString()
	first := &tokenv1.EntityScope{
		EntityType: tokenv1.EntityType_ENTITY_TYPE_RESOURCE,
		EntityId:   id,
		Scope:      tokenv1.Scope_SCOPE_READ,
	}
	for _, tc := range []struct {
		entityType tokenv1.EntityType
		scope      tokenv1.Scope
		valid      bool
	}{
		{tokenv1.EntityType_ENTITY_TYPE_RESOURCE, tokenv1.Scope_SCOPE_READ, false},
		{tokenv1.EntityType_ENTITY_TYPE_RESOURCE, tokenv1.Scope_SCOPE_WRITE, true},
		{tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, tokenv1.Scope_SCOPE_READ, true},
	} {
		req := &tokenv1.CreateTokenRequest{
			Name: "scope-identity", EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
			EntityId: id, ExpiresInSec: 60,
			Scopes: []*tokenv1.EntityScope{first, {EntityType: tc.entityType, EntityId: id, Scope: tc.scope}},
		}
		if err := protovalidate.Validate(req); (err == nil) != tc.valid {
			t.Fatalf("entity type %s scope %s: %v", tc.entityType, tc.scope, err)
		}
	}
}

func TestCreateMemberScopeValidation(t *testing.T) {
	for _, tc := range []struct {
		scopes []string
		valid  bool
	}{
		{nil, false},
		{[]string{genDb.ScopeRead}, true},
		{[]string{genDb.ScopeRead, genDb.ScopeWrite, genDb.ScopeAdmin}, true},
		{[]string{genDb.ScopeRead, genDb.ScopeRead}, false}, {[]string{genDb.ScopeMember}, false},
	} {
		req := &workspacev1.CreateMemberRequest{
			WorkspaceId: uuid.NewString(),
			UserId:      uuid.NewString(),
			Scopes:      tc.scopes,
		}
		if err := protovalidate.Validate(req); (err == nil) != tc.valid {
			t.Fatalf("scopes %v: %v", tc.scopes, err)
		}
	}
}
