package apps

import (
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestAccountDeletionInvalidatesPersonalCredentials(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	u := inviteLifecycleUser(t, p.User, w.OrgID, "deleted-member@test.local")
	grantLifecycleWorkspace(t, w.ID, u.id, "read", "write", "admin")
	personal := lifecycleToken(t, u.session, "personal", tokenv1.EntityType_ENTITY_TYPE_USER, u.id, lifecycleRead(w.ID))
	owned := lifecycleToken(
		t,
		u.session,
		"surviving",
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		w.ID,
		lifecycleRead(w.ID),
	)
	code := approveLifecycleCode(t, u.web)
	device, err := p.Clients("").Auth.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = u.web.Auth.ApproveDeviceLogin(t.Context(), &authv1.ApproveDeviceLoginRequest{
		UserCode: device.GetUserCode(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = u.session.User.DeleteUser(t.Context(), &userv1.DeleteUserRequest{UserId: u.id}); err != nil {
		t.Fatal(err)
	}
	readLifecycleWorkspace(t, u.session, w.ID, new(connect.CodeUnauthenticated))
	readLifecycleWorkspace(t, p.Clients(personal), w.ID, new(connect.CodeUnauthenticated))
	if _, err = p.Clients("").Auth.RefreshCLIToken(t.Context(), &authv1.RefreshCLITokenRequest{
		RefreshToken: u.tokens.GetRefreshToken(),
	}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("refresh after deletion = %v, want Unauthenticated", err)
	}
	_, err = p.Clients("").Auth.ExchangeCLICode(t.Context(), code)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("code exchange after deletion = %v, want Unauthenticated", err)
	}
	if _, err = p.Clients("").Auth.PollDeviceLogin(t.Context(), &authv1.PollDeviceLoginRequest{
		DeviceCode: device.GetDeviceCode(),
	}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("device exchange after deletion = %v, want Unauthenticated", err)
	}
	readLifecycleWorkspace(t, p.Clients(owned), w.ID, nil)
	members, err := p.User.Org.ListOrgMembers(t.Context(), &orgv1.ListOrgMembersRequest{OrgId: w.OrgID})
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range members.GetMembers() {
		if member.GetUser().GetId() == u.id {
			t.Fatal("deleted account remains an organization member")
		}
	}
	workspaceMembers, err := p.User.Workspace.ListWorkspaceMembers(
		t.Context(),
		&workspacev1.ListWorkspaceMembersRequest{
			WorkspaceId: w.ID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range workspaceMembers.GetMembers() {
		if member.GetUserId() == u.id {
			t.Fatal("deleted account remains a workspace member")
		}
	}
	again := signInLifecycleUser(t, "deleted-member@test.local")
	if again.id == u.id {
		t.Fatal("a later signup restored the deleted account")
	}
	readLifecycleWorkspace(t, again.session, w.ID, new(connect.CodePermissionDenied))
}

func TestAccountDeletionRequiresAnotherActiveAdmin(t *testing.T) {
	t.Parallel()
	u := signInLifecycleUser(t, "deleted-creator@test.local")
	const name = "e2e-account-deletion-admin"
	org, err := u.session.Org.CreateOrg(t.Context(), &orgv1.CreateOrgRequest{Name: new(name)})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := u.session.Workspace.CreateWorkspace(t.Context(), &workspacev1.CreateWorkspaceRequest{
		OrgId: org.GetOrgId(), Name: "surviving",
	})
	if err != nil {
		t.Fatal(err)
	}
	owned := lifecycleToken(t, u.session, "surviving", tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		workspace.GetWorkspaceId(), lifecycleRead(workspace.GetWorkspaceId()))
	personal := lifecycleToken(t, u.session, "personal", tokenv1.EntityType_ENTITY_TYPE_USER,
		u.id, lifecycleRead(workspace.GetWorkspaceId()))
	wantBlocked := func() {
		t.Helper()
		_, deleteErr := u.session.User.DeleteUser(t.Context(), &userv1.DeleteUserRequest{UserId: u.id})
		if connect.CodeOf(deleteErr) != connect.CodeFailedPrecondition || !strings.Contains(deleteErr.Error(), name) {
			t.Fatalf("delete last active admin = %v, want FailedPrecondition naming %s", deleteErr, name)
		}
		readLifecycleWorkspace(t, u.session, workspace.GetWorkspaceId(), nil)
		readLifecycleWorkspace(t, p.Clients(personal), workspace.GetWorkspaceId(), nil)
	}
	wantBlocked()
	successor := inviteLifecycleUser(t, u.session, org.GetOrgId(), "deletion-successor@test.local")
	setLifecycleRole(t, u.session, org.GetOrgId(), successor.id, orgv1.OrgRole_ORG_ROLE_ADMIN)
	setLifecycleStatus(t, successor.id, userv1.UserStatus_USER_STATUS_SUSPENDED)
	setLifecycleStatus(t, u.id, userv1.UserStatus_USER_STATUS_SUSPENDED)
	if _, err = p.Admin.Org.SetOrgMemberRole(t.Context(), &orgv1.SetOrgMemberRoleRequest{
		OrgId: org.GetOrgId(), UserId: u.id, Role: orgv1.OrgRole_ORG_ROLE_MEMBER,
	}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("demote suspended admin without active successor = %v, want FailedPrecondition", err)
	}
	if _, err = p.Admin.Org.RemoveOrgMember(t.Context(), &orgv1.RemoveOrgMemberRequest{
		OrgId: org.GetOrgId(), UserId: u.id,
	}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("remove suspended admin without active successor = %v, want FailedPrecondition", err)
	}
	setLifecycleStatus(t, u.id, userv1.UserStatus_USER_STATUS_ACTIVE)
	wantBlocked()
	setLifecycleStatus(t, successor.id, userv1.UserStatus_USER_STATUS_ACTIVE)
	if _, err = u.session.User.DeleteUser(t.Context(), &userv1.DeleteUserRequest{UserId: u.id}); err != nil {
		t.Fatal(err)
	}
	readLifecycleWorkspace(t, successor.session, workspace.GetWorkspaceId(), nil)
	readLifecycleWorkspace(t, p.Clients(owned), workspace.GetWorkspaceId(), nil)
	readLifecycleWorkspace(t, u.session, workspace.GetWorkspaceId(), new(connect.CodeUnauthenticated))
	readLifecycleWorkspace(t, p.Clients(personal), workspace.GetWorkspaceId(), new(connect.CodeUnauthenticated))
}
