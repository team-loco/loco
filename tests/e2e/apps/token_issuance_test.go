package apps

import (
	"testing"

	"connectrpc.com/connect/v2"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/internal/rpc"
)

func TestTokenIssuanceRequiresOwnerPermissionAndBoundedScopes(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	other := w.NewWorkspace(t, "other")
	u := inviteLifecycleUser(t, p.User, w.OrgID, "token-issuance@test.local")
	setLifecycleRole(t, p.User, w.OrgID, u.id, orgv1.OrgRole_ORG_ROLE_ADMIN)
	adminScopes := make([]*tokenv1.EntityScope, 0, 3)
	for _, scope := range []tokenv1.Scope{tokenv1.Scope_SCOPE_READ, tokenv1.Scope_SCOPE_WRITE, tokenv1.Scope_SCOPE_ADMIN} {
		adminScopes = append(
			adminScopes,
			&tokenv1.EntityScope{EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, EntityId: w.ID, Scope: scope},
		)
	}
	personal := lifecycleToken(
		t,
		u.session,
		"personal-admin",
		tokenv1.EntityType_ENTITY_TYPE_USER,
		u.id,
		adminScopes...)
	owned := lifecycleToken(t, u.session, "owned-admin", tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, w.ID, adminScopes...)
	adminOnly := lifecycleToken(
		t,
		u.session,
		"admin-only",
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		w.ID,
		&tokenv1.EntityScope{
			EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
			EntityId:   w.ID,
			Scope:      tokenv1.Scope_SCOPE_ADMIN,
		},
	)
	denyLifecycleToken(
		t,
		p.Clients(adminOnly),
		"admin-is-not-read",
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		w.ID,
		lifecycleRead(w.ID),
	)
	for name, credential := range map[string]string{"personal": personal, "workspace": owned} {
		if !t.Run(name, func(t *testing.T) {
			client := p.Clients(credential)
			child := lifecycleToken(
				t,
				client,
				name+"-delegated-reader",
				tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
				w.ID,
				lifecycleRead(w.ID),
			)
			readLifecycleWorkspace(t, p.Clients(child), w.ID, nil)
			readLifecycleWorkspace(t, p.Clients(child), other.ID, new(connect.CodePermissionDenied))
			denyLifecycleToken(
				t,
				p.Clients(child),
				"reader-cannot-escalate",
				tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
				w.ID,
				&tokenv1.EntityScope{
					EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
					EntityId:   w.ID,
					Scope:      tokenv1.Scope_SCOPE_WRITE,
				},
			)
			denyLifecycleToken(
				t,
				client,
				name+"-sibling-owner",
				tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
				other.ID,
				lifecycleRead(other.ID),
			)
			denyLifecycleToken(
				t,
				client,
				name+"-sibling-scope",
				tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
				w.ID,
				lifecycleRead(other.ID),
			)
			denyLifecycleToken(
				t,
				client,
				name+"-ancestor-owner",
				tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION,
				w.OrgID,
				lifecycleRead(w.ID),
			)
		}) {
			return
		}
	}
	// Organization admin is sufficient for the sibling scope, but its requested workspace
	// owner must still contain every delegated scope.
	denyLifecycleToken(
		t,
		u.session,
		"sibling-scope",
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		w.ID,
		lifecycleRead(other.ID),
	)
	denyLifecycleToken(
		t,
		p.Clients(owned),
		"issuer-is-not-a-personal-owner",
		tokenv1.EntityType_ENTITY_TYPE_USER,
		u.id,
		lifecycleRead(w.ID),
	)
	setLifecycleRole(t, p.User, w.OrgID, u.id, orgv1.OrgRole_ORG_ROLE_MEMBER)
	grantLifecycleWorkspace(t, w.ID, u.id, "read", "write")
	denyLifecycleToken(
		t,
		u.session,
		"writer-cannot-issue",
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		w.ID,
		lifecycleRead(w.ID),
	)
	denyLifecycleToken(
		t,
		p.Clients(personal),
		"personal-lost-admin",
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		w.ID,
		lifecycleRead(w.ID),
	)
	if _, err := p.User.Org.RemoveOrgMember(
		t.Context(),
		&orgv1.RemoveOrgMemberRequest{OrgId: w.OrgID, UserId: u.id},
	); err != nil {
		t.Fatal(err)
	}
	survivingChild := lifecycleToken(
		t,
		p.Clients(owned),
		"delegated-after-issuer-departure",
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		w.ID,
		lifecycleRead(w.ID),
	)
	readLifecycleWorkspace(t, p.Clients(survivingChild), w.ID, nil)
	denyLifecycleToken(
		t,
		p.Clients(owned),
		"departed-issuer-is-not-a-personal-owner",
		tokenv1.EntityType_ENTITY_TYPE_USER,
		u.id,
		lifecycleRead(w.ID),
	)
	denyLifecycleToken(
		t,
		p.Clients(owned),
		"departed-issuer-has-no-sibling-authority",
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		other.ID,
		lifecycleRead(other.ID),
	)
}

func denyLifecycleToken(t *testing.T, clients *rpc.Clients, name string, owner tokenv1.EntityType,
	ownerID string, scopes ...*tokenv1.EntityScope,
) {
	t.Helper()
	_, err := clients.Token.CreateToken(t.Context(), &tokenv1.CreateTokenRequest{
		Name: name, EntityType: owner, EntityId: ownerID, Scopes: scopes, ExpiresInSec: 3600,
	})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("create %s token = %v, want PermissionDenied", name, err)
	}
}
