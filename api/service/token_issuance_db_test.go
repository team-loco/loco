package service

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
)

func tokenRequest(target genDb.Entity, scopes ...genDb.Scope) *tokenv1.CreateTokenRequest {
	requested := make([]*tokenv1.EntityScope, len(scopes))
	for i, scope := range scopes {
		requested[i] = &tokenv1.EntityScope{
			EntityType: dbEntityTypeToProto(target.Type),
			EntityId:   target.ID.String(),
			Scope:      dbScopeToProto(scope),
		}
	}
	return &tokenv1.CreateTokenRequest{
		Name:         uuid.NewString(),
		EntityType:   dbEntityTypeToProto(target.Type),
		EntityId:     target.ID.String(),
		Scopes:       requested,
		ExpiresInSec: 60,
	}
}

func TestCreateTokenRequiresSession(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	server := NewTokenServer(f.pool, f.queries, f.machine, limits.Defaults())
	user := genDb.Entity{Type: genDb.EntityTypeUser, ID: f.ownerID}
	org := genDb.Entity{Type: genDb.EntityTypeOrganization, ID: f.orgID}
	workspace := genDb.Entity{Type: genDb.EntityTypeWorkspace, ID: f.workspaceID}
	resource := genDb.Entity{Type: genDb.EntityTypeResource, ID: f.resourceID}
	admin := []genDb.Scope{genDb.ScopeRead, genDb.ScopeWrite, genDb.ScopeAdmin}
	for _, target := range []genDb.Entity{user, org, workspace, resource} {
		created, err := server.CreateToken(f.owner, tokenRequest(target, admin...))
		if err != nil {
			t.Fatalf("session creates %s token: %v", target.Type, err)
		}
		if created.GetTokenMetadata().GetCreatedBy() != f.ownerID.String() {
			t.Fatalf("issuer = %s, want %s", created.GetTokenMetadata().GetCreatedBy(), f.ownerID)
		}
	}
	personalRequest := tokenRequest(user, genDb.ScopeRead)
	personalRequest.Scopes = append(
		tokenRequest(user, genDb.ScopeRead, genDb.ScopeWrite).GetScopes(),
		tokenRequest(org, admin...).GetScopes()...,
	)
	personal, err := server.CreateToken(f.owner, personalRequest)
	if err != nil {
		t.Fatal(err)
	}
	owned, err := server.CreateToken(f.owner, tokenRequest(workspace, admin...))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range map[string]struct {
		token   string
		targets []genDb.Entity
	}{
		"personal": {personal.GetToken(), []genDb.Entity{user, org, workspace}},
		"owned":    {owned.GetToken(), []genDb.Entity{workspace}},
	} {
		caller := issuanceTokenContext(t, f, test.token)
		for _, target := range test.targets {
			_, err = server.CreateToken(caller, tokenRequest(target, genDb.ScopeRead))
			wantSessionRequired(t, err)
		}
	}
	other := f.signUp(t, "other", "other@tokens.test")
	p, _ := reqctx.PrincipalFrom(f.owner)
	p.Scopes = append(
		p.Scopes,
		genDb.EntityScope{EntityType: genDb.EntityTypeUser, EntityID: other, Scope: genDb.ScopeWrite},
	)
	_, err = server.CreateToken(
		reqctx.WithPrincipal(f.owner, p),
		tokenRequest(genDb.Entity{Type: genDb.EntityTypeUser, ID: other}, genDb.ScopeRead),
	)
	wantCode(t, err, connect.CodePermissionDenied)
}

func TestEntityTokenRequiresAdmin(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	server := NewTokenServer(f.pool, f.queries, f.machine, limits.Defaults())
	for _, target := range []genDb.Entity{
		{Type: genDb.EntityTypeOrganization, ID: f.orgID},
		{Type: genDb.EntityTypeWorkspace, ID: f.workspaceID},
		{Type: genDb.EntityTypeResource, ID: f.resourceID},
	} {
		t.Run(string(target.Type), func(t *testing.T) {
			ctx := f.owner
			p, _ := reqctx.PrincipalFrom(ctx)
			p.Scopes = []genDb.EntityScope{{EntityType: target.Type, EntityID: target.ID, Scope: genDb.ScopeWrite}}
			_, err := server.CreateToken(reqctx.WithPrincipal(ctx, p), tokenRequest(target, genDb.ScopeRead))
			wantCode(t, err, connect.CodePermissionDenied)
			p.Scopes = []genDb.EntityScope{{EntityType: target.Type, EntityID: target.ID, Scope: genDb.ScopeAdmin}}
			if _, err := server.CreateToken(
				reqctx.WithPrincipal(ctx, p),
				tokenRequest(target, genDb.ScopeRead),
			); err != nil {
				t.Fatalf("admin: %v", err)
			}
		})
	}
}

func TestTokenScopesStayUnderTarget(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	server := NewTokenServer(f.pool, f.queries, f.machine, limits.Defaults())
	sibling := f.workspace(t, "sibling")
	otherOrg, err := f.queries.CreateOrg(
		t.Context(),
		genDb.CreateOrgParams{Name: "outside-token-target", CreatedBy: f.ownerID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.queries.AddUserScope(
		t.Context(),
		genDb.AddUserScopeParams{
			UserID:     f.ownerID,
			EntityType: genDb.EntityTypeOrganization,
			EntityID:   otherOrg.ID,
			Scope:      genDb.ScopeRead,
		},
	); err != nil {
		t.Fatal(err)
	}
	org := genDb.Entity{Type: genDb.EntityTypeOrganization, ID: f.orgID}
	workspace := genDb.Entity{Type: genDb.EntityTypeWorkspace, ID: f.workspaceID}
	resource := genDb.Entity{Type: genDb.EntityTypeResource, ID: f.resourceID}
	for _, test := range []struct {
		name              string
		target, requested genDb.Entity
		allowed           bool
	}{
		{"org itself", org, org, true},
		{"org workspace", org, workspace, true},
		{"org resource", org, resource, true},
		{"workspace itself", workspace, workspace, true},
		{"workspace resource", workspace, resource, true},
		{"resource itself", resource, resource, true},
		{"workspace parent", workspace, org, false},
		{"resource parent", resource, workspace, false},
		{"workspace sibling", workspace, genDb.Entity{Type: genDb.EntityTypeWorkspace, ID: sibling}, false},
		{"foreign organization", org, genDb.Entity{Type: genDb.EntityTypeOrganization, ID: otherOrg.ID}, false},
		{"issuing user", org, genDb.Entity{Type: genDb.EntityTypeUser, ID: f.ownerID}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := tokenRequest(test.target, genDb.ScopeRead)
			request.Scopes = tokenRequest(test.requested, genDb.ScopeRead).GetScopes()
			_, err := server.CreateToken(f.owner, request)
			if test.allowed {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				wantCode(t, err, connect.CodePermissionDenied)
			}
		})
	}
}

func TestRevokeOwnTokenShortcutRequiresSession(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	server := NewTokenServer(f.pool, f.queries, f.machine, limits.Defaults())
	target := genDb.Entity{Type: genDb.EntityTypeUser, ID: f.ownerID}
	session := f.owner
	victim, err := server.CreateToken(session, tokenRequest(target, genDb.ScopeRead))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := server.CreateToken(session, tokenRequest(target, genDb.ScopeRead))
	if err != nil {
		t.Fatal(err)
	}
	request := &tokenv1.RevokeTokenRequest{
		Name:       victim.GetTokenMetadata().GetName(),
		EntityType: dbEntityTypeToProto(target.Type),
		EntityId:   target.ID.String(),
	}
	_, err = server.RevokeToken(issuanceTokenContext(t, f, reader.GetToken()), request)
	wantCode(t, err, connect.CodePermissionDenied)
	if _, err = f.machine.GetToken(t.Context(), victim.GetToken()); err != nil {
		t.Fatalf("target token: %v", err)
	}
	if _, err = server.RevokeToken(session, request); err != nil {
		t.Fatalf("session revoke: %v", err)
	}
	admin, err := server.CreateToken(session, tokenRequest(target, genDb.ScopeAdmin))
	if err != nil {
		t.Fatal(err)
	}
	victim, err = server.CreateToken(session, tokenRequest(target, genDb.ScopeRead))
	if err != nil {
		t.Fatal(err)
	}
	request.Name = victim.GetTokenMetadata().GetName()
	if _, err = server.RevokeToken(issuanceTokenContext(t, f, admin.GetToken()), request); err != nil {
		t.Fatalf("explicit token administrator revoke: %v", err)
	}
	for _, target := range []genDb.Entity{
		{Type: genDb.EntityTypeOrganization, ID: f.orgID},
		{Type: genDb.EntityTypeWorkspace, ID: f.workspaceID},
		{Type: genDb.EntityTypeResource, ID: f.resourceID},
	} {
		issued, err := server.CreateToken(session, tokenRequest(target, genDb.ScopeRead))
		if err != nil {
			t.Fatal(err)
		}
		request := &tokenv1.RevokeTokenRequest{
			Name:       issued.GetTokenMetadata().GetName(),
			EntityType: dbEntityTypeToProto(target.Type),
			EntityId:   target.ID.String(),
		}
		p, _ := reqctx.PrincipalFrom(session)
		p.Scopes = []genDb.EntityScope{{EntityType: target.Type, EntityID: target.ID, Scope: genDb.ScopeWrite}}
		_, err = server.RevokeToken(reqctx.WithPrincipal(session, p), request)
		wantCode(t, err, connect.CodePermissionDenied)
		p.Scopes[0].Scope = genDb.ScopeAdmin
		if _, err := server.RevokeToken(reqctx.WithPrincipal(session, p), request); err != nil {
			t.Fatalf("admin revoke: %v", err)
		}
	}
}

func wantSessionRequired(t *testing.T, err error) {
	t.Helper()
	if denied, ok := errors.AsType[*connect.Error](err); !ok ||
		denied.Code() != connect.CodePermissionDenied || denied.Message() != publicSessionRequired {
		t.Fatalf("error = %v, want PermissionDenied for a missing session", err)
	}
}

func issuanceTokenContext(t *testing.T, f *secretFixture, token string) context.Context {
	t.Helper()
	p, err := f.machine.GetToken(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	return reqctx.WithPrincipal(t.Context(), p)
}
