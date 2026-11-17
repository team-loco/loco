package apps

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"connectrpc.com/connect/v2"

	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/rpc"
)

type lifecycleUser struct {
	id      string
	web     *rpc.Clients
	session *rpc.Clients
	tokens  *authv1.CLITokens
}

func signInLifecycleUser(t *testing.T, email string) lifecycleUser {
	t.Helper()
	web := p.Clients(p.ProviderToken(t, email))
	code := approveLifecycleCode(t, web)
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

func approveLifecycleCode(t *testing.T, web *rpc.Clients) *authv1.ExchangeCLICodeRequest {
	t.Helper()
	verifier := bearer.New("")
	sum := sha256.Sum256([]byte(verifier))
	response, err := web.Auth.ApproveCLILogin(t.Context(), &authv1.ApproveCLILoginRequest{
		State: bearer.New(""), CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:]),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &authv1.ExchangeCLICodeRequest{Code: response.GetCode(), CodeVerifier: verifier}
}

func lifecycleToken(t *testing.T, clients *rpc.Clients, name string, owner tokenv1.EntityType,
	ownerID string, scopes ...*tokenv1.EntityScope,
) string {
	t.Helper()
	response, err := clients.Token.CreateToken(t.Context(), &tokenv1.CreateTokenRequest{
		Name: name, EntityType: owner, EntityId: ownerID, Scopes: scopes, ExpiresInSec: 3600,
	})
	if err != nil {
		t.Fatalf("create %s token: %v", name, err)
	}
	return response.GetToken()
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
