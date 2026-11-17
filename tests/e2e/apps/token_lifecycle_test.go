package apps

import (
	"testing"

	"connectrpc.com/connect/v2"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestPersonalAndOwnedTokenLifecycle(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	other := w.NewWorkspace(t, "other")
	u := inviteLifecycleUser(t, p.User, w.OrgID, "token-lifecycle@test.local")
	setLifecycleRole(t, p.User, w.OrgID, u.id, orgv1.OrgRole_ORG_ROLE_ADMIN)
	orgScope := &tokenv1.EntityScope{
		EntityType: tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION,
		EntityId:   w.OrgID,
		Scope:      tokenv1.Scope_SCOPE_READ,
	}
	broad := lifecycleToken(t, u.session, "organization-ceiling", tokenv1.EntityType_ENTITY_TYPE_USER, u.id, orgScope)
	narrow := lifecycleToken(
		t,
		u.session,
		"workspace-ceiling",
		tokenv1.EntityType_ENTITY_TYPE_USER,
		u.id,
		lifecycleRead(w.ID),
	)
	owned := lifecycleToken(
		t,
		u.session,
		"automation",
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		w.ID,
		lifecycleRead(w.ID),
	)
	setLifecycleRole(t, p.User, w.OrgID, u.id, orgv1.OrgRole_ORG_ROLE_MEMBER)
	grantLifecycleWorkspace(t, w.ID, u.id, "read", "write")
	// A stored organization scope and a live workspace grant retain only their intersection.
	readLifecycleWorkspace(t, p.Clients(broad), w.ID, nil)
	readLifecycleWorkspace(t, p.Clients(broad), other.ID, new(connect.CodePermissionDenied))
	grantLifecycleWorkspace(t, other.ID, u.id, "read")
	readLifecycleWorkspace(t, p.Clients(broad), other.ID, nil)
	readLifecycleWorkspace(t, p.Clients(narrow), other.ID, new(connect.CodePermissionDenied))
	if _, err := p.User.Workspace.DeleteMember(t.Context(), &workspacev1.DeleteMemberRequest{
		WorkspaceId: w.ID, UserId: u.id,
	}); err != nil {
		t.Fatal(err)
	}
	readLifecycleWorkspace(t, p.Clients(broad), w.ID, new(connect.CodePermissionDenied))
	readLifecycleWorkspace(t, p.Clients(narrow), w.ID, new(connect.CodePermissionDenied))
	setLifecycleStatus(t, u.id, userv1.UserStatus_USER_STATUS_SUSPENDED)
	readLifecycleWorkspace(t, u.session, other.ID, new(connect.CodeUnauthenticated))
	readLifecycleWorkspace(t, p.Clients(broad), other.ID, new(connect.CodeUnauthenticated))
	readLifecycleWorkspace(t, p.Clients(owned), w.ID, nil)
	setLifecycleStatus(t, u.id, userv1.UserStatus_USER_STATUS_ACTIVE)
	if _, err := p.User.Org.RemoveOrgMember(t.Context(), &orgv1.RemoveOrgMemberRequest{
		OrgId: w.OrgID, UserId: u.id,
	}); err != nil {
		t.Fatal(err)
	}
	readLifecycleWorkspace(t, p.Clients(broad), other.ID, new(connect.CodePermissionDenied))
	readLifecycleWorkspace(t, p.Clients(owned), w.ID, nil)
	if _, err := p.User.Token.GetToken(t.Context(), &tokenv1.GetTokenRequest{
		Name: "automation", EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, EntityId: w.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.User.Token.RevokeToken(t.Context(), &tokenv1.RevokeTokenRequest{
		Name: "automation", EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, EntityId: w.ID,
	}); err != nil {
		t.Fatal(err)
	}
	readLifecycleWorkspace(t, p.Clients(owned), w.ID, new(connect.CodeUnauthenticated))
	ownerDeleted := other.MintToken(t, "owner-deleted", tokenv1.Scope_SCOPE_READ)
	if _, err := p.User.Workspace.DeleteWorkspace(t.Context(), &workspacev1.DeleteWorkspaceRequest{
		WorkspaceId: other.ID, ConfirmDeleteApps: true,
	}); err != nil {
		t.Fatal(err)
	}
	readLifecycleWorkspace(t, p.Clients(ownerDeleted), w.ID, new(connect.CodeUnauthenticated))
}
