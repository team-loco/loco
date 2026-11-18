package apps

import (
	"testing"

	"connectrpc.com/connect/v2"

	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/internal/rpc"
	"github.com/team-loco/loco/tests/e2e/platform"
)

// everyScope is the read, write and admin scope.
var everyScope = []tokenv1.Scope{tokenv1.Scope_SCOPE_READ, tokenv1.Scope_SCOPE_WRITE, tokenv1.Scope_SCOPE_ADMIN}

type lifecycleUser struct {
	id      string
	web     *rpc.Clients
	session *rpc.Clients
	tokens  *authv1.CLITokens
}

func signInLifecycleUser(t *testing.T, email string) lifecycleUser {
	t.Helper()
	web := p.Clients(p.ProviderToken(t, email))
	code, err := platform.ApproveCLILogin(t.Context(), web)
	if err != nil {
		t.Fatal(err)
	}
	exchanged, err := p.Clients("").Auth.ExchangeCLICode(t.Context(), code)
	if err != nil {
		t.Fatal(err)
	}
	session := p.Clients(exchanged.GetTokens().GetAccessToken())
	me, err := session.User.WhoAmI(t.Context(), &userv1.WhoAmIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return lifecycleUser{id: me.GetUser().GetId(), web: web, session: session, tokens: exchanged.GetTokens()}
}

func inviteLifecycleUser(t *testing.T, admin *rpc.Clients, orgID, email string) lifecycleUser {
	t.Helper()
	if _, err := admin.Org.InviteOrgMember(t.Context(), &orgv1.InviteOrgMemberRequest{
		OrgId: orgID, Email: email,
	}); err != nil {
		t.Fatal(err)
	}
	return signInLifecycleUser(t, email)
}

func lifecycleRead(workspaceID string) *tokenv1.EntityScope {
	return &tokenv1.EntityScope{
		EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, EntityId: workspaceID, Scope: tokenv1.Scope_SCOPE_READ,
	}
}

func setLifecycleRole(t *testing.T, admin *rpc.Clients, orgID, userID string, role orgv1.OrgRole) {
	t.Helper()
	if _, err := admin.Org.SetOrgMemberRole(t.Context(), &orgv1.SetOrgMemberRoleRequest{
		OrgId: orgID, UserId: userID, Role: role,
	}); err != nil {
		t.Fatal(err)
	}
}

func grantLifecycleWorkspace(t *testing.T, workspaceID, userID string, scopes ...string) {
	t.Helper()
	if _, err := p.User.Workspace.CreateMember(t.Context(), &workspacev1.CreateMemberRequest{
		WorkspaceId: workspaceID, UserId: userID, Scopes: scopes,
	}); err != nil {
		t.Fatal(err)
	}
}

func setLifecycleStatus(t *testing.T, userID string, status userv1.UserStatus) {
	t.Helper()
	if _, err := p.Admin.User.SetUserStatus(t.Context(), &userv1.SetUserStatusRequest{
		Key: &userv1.SetUserStatusRequest_UserId{UserId: userID}, Status: status,
	}); err != nil {
		t.Fatal(err)
	}
}

func readLifecycleWorkspace(t *testing.T, clients *rpc.Clients, workspaceID string, want *connect.Code) {
	t.Helper()
	_, err := clients.Workspace.GetWorkspace(t.Context(), &workspacev1.GetWorkspaceRequest{WorkspaceId: workspaceID})
	if want == nil {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if err == nil || connect.CodeOf(err) != *want {
		t.Fatalf("read workspace = %v, want %s", err, *want)
	}
}
