package service

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/validate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/authz"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	"github.com/team-loco/loco/api/tvm"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/rpc"
)

const (
	membershipSessionDuration = time.Hour
	membershipOrgName         = "acme"
	membershipSharedPath      = membershipOrgName + "/shared"
)

type membershipAPI struct {
	pool     *pgxpool.Pool
	queries  *genDb.Queries
	machine  *tvm.VendingMachine
	resolver *auth.Resolver
	url      string
}

type membershipUser struct {
	id      uuid.UUID
	token   string
	clients *rpc.Clients
}

type membershipOrg struct {
	api     *membershipAPI
	owner   membershipUser
	orgID   string
	shared  string
	sibling string
}

func newMembershipAPI(t *testing.T) *membershipAPI {
	t.Helper()
	return newMembershipAPIWith(t, InvitationConfig{})
}

func newMembershipAPIWith(t *testing.T, invitations InvitationConfig) *membershipAPI {
	t.Helper()
	pool := authtest.NewPool(t)
	queries := genDb.New(pool)
	machine := tvm.NewVendingMachine(pool, queries, tvm.Config{
		SessionAccessTokenDuration:  membershipSessionDuration,
		SessionRefreshTokenDuration: membershipSessionDuration,
		LastUsedUpdateInterval:      membershipSessionDuration,
	})
	t.Cleanup(machine.Close)
	policy, err := auth.ParseSignupPolicy("open", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	authorizer := authz.New(pool, queries)
	server := connect.NewServer(
		interceptor.NewErrorInterceptor(),
		interceptor.NewContextInterceptor().Interceptor(),
		interceptor.NewAuthInterceptor(machine, nil, nil, authorizer).Interceptor(),
		validate.NewServerInterceptor(),
	)
	orgv1connect.RegisterOrgServiceHandler(server, NewOrgServer(pool, queries, machine, invitations))
	workspacev1connect.RegisterWorkspaceServiceHandler(server, NewWorkspaceServer(pool, queries, machine))
	userv1connect.RegisterUserServiceHandler(server, NewUserServer(pool, queries, machine, auth.Admins{}))
	resources := NewResourceServer(pool, queries, servicedefaults.Defaults{}, time.Minute)
	resourcev1connect.RegisterResourceServiceHandler(server, resources)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &membershipAPI{
		pool:     pool,
		queries:  queries,
		machine:  machine,
		resolver: auth.NewResolver(pool, policy),
		url:      srv.URL,
	}
}

func (a *membershipAPI) signUp(t *testing.T, subject string) membershipUser {
	t.Helper()
	user, err := a.resolver.Resolve(t.Context(), auth.Identity{
		Issuer: testIssuer, Subject: subject, Email: subject + "@acme.test", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("sign up %s: %v", subject, err)
	}
	token, _, err := a.machine.IssueSession(t.Context(), user.ID, nil, "", "test")
	if err != nil {
		t.Fatalf("session for %s: %v", subject, err)
	}
	clients := rpc.NewHTTPClients(http.DefaultClient, a.url, token)
	return membershipUser{id: user.ID, token: token, clients: clients}
}

func newMembershipOrg(t *testing.T) *membershipOrg {
	t.Helper()
	return newMembershipOrgWith(t, InvitationConfig{})
}

func newMembershipOrgWith(t *testing.T, invitations InvitationConfig) *membershipOrg {
	t.Helper()
	api := newMembershipAPIWith(t, invitations)
	owner := api.signUp(t, "owner")
	org, err := owner.clients.Org.CreateOrg(t.Context(), &orgv1.CreateOrgRequest{Name: new(membershipOrgName)})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	o := &membershipOrg{api: api, owner: owner, orgID: org.GetOrgId()}
	o.shared = o.createWorkspace(t, "shared")
	o.sibling = o.createWorkspace(t, "sibling")
	return o
}

func (o *membershipOrg) createWorkspace(t *testing.T, name string) string {
	t.Helper()
	ws, err := o.owner.clients.Workspace.CreateWorkspace(t.Context(), &workspacev1.CreateWorkspaceRequest{
		OrgId: o.orgID, Name: name,
	})
	if err != nil {
		t.Fatalf("create workspace %s: %v", name, err)
	}
	return ws.GetWorkspaceId()
}

func (o *membershipOrg) addOrgMember(t *testing.T, user membershipUser) {
	t.Helper()
	_, err := o.owner.clients.Org.AddOrgMember(t.Context(), &orgv1.AddOrgMemberRequest{
		OrgId: o.orgID, UserId: user.id.String(),
	})
	if err != nil {
		t.Fatalf("add org member: %v", err)
	}
}

func (o *membershipOrg) grantWorkspace(t *testing.T, user membershipUser, workspaceID string) {
	t.Helper()
	_, err := o.owner.clients.Workspace.CreateMember(t.Context(), &workspacev1.CreateMemberRequest{
		WorkspaceId: workspaceID, UserId: user.id.String(), Scopes: []string{genDb.ScopeRead, genDb.ScopeWrite},
	})
	if err != nil {
		t.Fatalf("grant workspace: %v", err)
	}
}

func userOrgIDs(t *testing.T, user membershipUser) []string {
	t.Helper()
	resp, err := user.clients.Org.ListUserOrgs(t.Context(), &orgv1.ListUserOrgsRequest{UserId: user.id.String()})
	if err != nil {
		t.Fatalf("list user orgs: %v", err)
	}
	ids := make([]string, 0, len(resp.GetOrgs()))
	for _, org := range resp.GetOrgs() {
		ids = append(ids, org.GetId())
	}
	return ids
}

func TestOrgMemberSeesOrgButNoWorkspace(t *testing.T) {
	o := newMembershipOrg(t)
	member := o.api.signUp(t, "member")
	o.addOrgMember(t, member)
	ctx := t.Context()

	if ids := userOrgIDs(t, member); !slices.Equal(ids, []string{o.orgID}) {
		t.Fatalf("member orgs = %v, want [%s]", ids, o.orgID)
	}
	getOrg := &orgv1.GetOrgRequest{Key: &orgv1.GetOrgRequest_OrgId{OrgId: o.orgID}}
	if _, err := member.clients.Org.GetOrg(ctx, getOrg); err != nil {
		t.Fatalf("member get org: %v", err)
	}
	members, err := member.clients.Org.ListOrgMembers(ctx, &orgv1.ListOrgMembersRequest{OrgId: o.orgID})
	if err != nil {
		t.Fatalf("member list org members: %v", err)
	}
	scopesByUser := map[string][]string{}
	for _, m := range members.GetMembers() {
		scopesByUser[m.GetUser().GetId()] = m.GetScopes()
	}
	if got := scopesByUser[member.id.String()]; !slices.Equal(got, []string{genDb.ScopeMember}) {
		t.Fatalf("member scopes = %v", got)
	}
	if got := scopesByUser[o.owner.id.String()]; len(got) != 3 {
		t.Fatalf("owner scopes = %v", got)
	}

	for _, workspaceID := range []string{o.shared, o.sibling} {
		_, err = member.clients.Workspace.GetWorkspace(ctx, &workspacev1.GetWorkspaceRequest{WorkspaceId: workspaceID})
		if codeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("member read workspace %s: %v", workspaceID, err)
		}
		_, err = member.clients.Resource.ListWorkspaceResources(ctx, &resourcev1.ListWorkspaceResourcesRequest{
			WorkspaceId: workspaceID,
		})
		if codeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("member listed resources of %s: %v", workspaceID, err)
		}
	}
	_, err = member.clients.Workspace.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{
		OrgId: o.orgID, Name: "mine",
	})
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("member created a workspace: %v", err)
	}
}

func TestOnlyOrgAdminsManageOrgMembers(t *testing.T) {
	o := newMembershipOrg(t)
	member := o.api.signUp(t, "member")
	outsider := o.api.signUp(t, "outsider")
	o.addOrgMember(t, member)

	_, err := member.clients.Org.AddOrgMember(t.Context(), &orgv1.AddOrgMemberRequest{
		OrgId: o.orgID, UserId: outsider.id.String(),
	})
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("member added a member: %v", err)
	}
	_, err = outsider.clients.Org.ListOrgMembers(t.Context(), &orgv1.ListOrgMembersRequest{OrgId: o.orgID})
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("outsider listed members: %v", err)
	}
	_, err = o.owner.clients.Org.AddOrgMember(t.Context(), &orgv1.AddOrgMemberRequest{
		OrgId: o.orgID, UserId: uuid.NewString(),
	})
	if codeOf(err) != connect.CodeNotFound {
		t.Fatalf("added an unknown user: %v", err)
	}
	_, err = o.owner.clients.Org.RemoveOrgMember(t.Context(), &orgv1.RemoveOrgMemberRequest{
		OrgId: o.orgID, UserId: o.owner.id.String(),
	})
	if codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("owner removed themselves: %v", err)
	}
}

func TestRemoveOrgMemberRevokesTheirGrantsInTheOrg(t *testing.T) {
	o := newMembershipOrg(t)
	member := o.api.signUp(t, "member")
	o.addOrgMember(t, member)
	o.grantWorkspace(t, member, o.shared)
	ctx := t.Context()

	other, err := member.clients.Org.CreateOrg(ctx, &orgv1.CreateOrgRequest{Name: new("member-org")})
	if err != nil {
		t.Fatalf("member creates own org: %v", err)
	}
	_, err = member.clients.Workspace.GetWorkspace(ctx, &workspacev1.GetWorkspaceRequest{WorkspaceId: o.shared})
	if err != nil {
		t.Fatalf("granted member read workspace: %v", err)
	}
	var resourceID uuid.UUID
	err = o.api.pool.QueryRow(ctx, `
INSERT INTO resources (workspace_id, name, type, description, spec, spec_version)
VALUES ($1, 'svc', 'service', '', '{}', 1) RETURNING id`, uuid.MustParse(o.shared)).Scan(&resourceID)
	if err != nil {
		t.Fatalf("seed resource: %v", err)
	}
	err = o.api.queries.AddUserScope(ctx, genDb.AddUserScopeParams{
		UserID: member.id, EntityType: genDb.EntityTypeResource, EntityID: resourceID, Scope: genDb.ScopeRead,
	})
	if err != nil {
		t.Fatalf("grant resource: %v", err)
	}

	_, err = o.owner.clients.Org.RemoveOrgMember(ctx, &orgv1.RemoveOrgMemberRequest{
		OrgId: o.orgID, UserId: member.id.String(),
	})
	if err != nil {
		t.Fatalf("remove org member: %v", err)
	}

	_, err = member.clients.Workspace.GetWorkspace(ctx, &workspacev1.GetWorkspaceRequest{WorkspaceId: o.shared})
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("removed member still reads the workspace: %v", err)
	}
	if ids := userOrgIDs(t, member); !slices.Equal(ids, []string{other.GetOrgId()}) {
		t.Fatalf("removed member orgs = %v, want only [%s]", ids, other.GetOrgId())
	}
	remaining, err := o.api.queries.GetUserScopes(ctx, member.id)
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	for _, scope := range remaining {
		if scope.EntityID.String() == o.orgID || scope.EntityID.String() == o.shared || scope.EntityID == resourceID {
			t.Fatalf("grant left behind: %+v", scope)
		}
	}
}

func TestWorkspaceOnlyMemberListsWorkspaceUnderItsOrg(t *testing.T) {
	o := newMembershipOrg(t)
	member := o.api.signUp(t, "member")
	o.addOrgMember(t, member)
	o.grantWorkspace(t, member, o.shared)
	apiClient := client.NewClient(o.api.url, member.token)

	workspaces, err := apiClient.GetUserWorkspaces(t.Context())
	if err != nil {
		t.Fatalf("list workspaces: %v", err)
	}
	i := slices.IndexFunc(workspaces, func(ws *workspacev1.Workspace) bool {
		return ws.GetOrgName()+"/"+ws.GetName() == membershipSharedPath
	})
	if i < 0 || workspaces[i].GetId() != o.shared {
		t.Fatalf("workspaces = %v, want %s with id %s", workspaceNames(workspaces), membershipSharedPath, o.shared)
	}
}

func workspaceNames(list []*workspacev1.Workspace) []string {
	names := make([]string, len(list))
	for i, ws := range list {
		names[i] = ws.GetOrgName() + "/" + ws.GetName()
	}
	slices.Sort(names)
	return names
}

func TestCreateMemberRequiresOrgMembership(t *testing.T) {
	o := newMembershipOrg(t)
	outsider := o.api.signUp(t, "outsider")
	request := &workspacev1.CreateMemberRequest{
		WorkspaceId: o.shared, UserId: outsider.id.String(), Scopes: []string{genDb.ScopeRead},
	}

	_, err := o.owner.clients.Workspace.CreateMember(t.Context(), request)
	if codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("granted a workspace to a non-member: %v", err)
	}
	remaining, err := o.api.queries.GetUserScopes(t.Context(), outsider.id)
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	for _, scope := range remaining {
		if scope.EntityType == genDb.EntityTypeWorkspace {
			t.Fatalf("rejected grant left a scope: %+v", scope)
		}
	}

	unknown := &workspacev1.CreateMemberRequest{
		WorkspaceId: o.shared, UserId: uuid.NewString(), Scopes: []string{genDb.ScopeRead},
	}
	_, err = o.owner.clients.Workspace.CreateMember(t.Context(), unknown)
	if codeOf(err) != connect.CodeNotFound {
		t.Fatalf("granted a workspace to an unknown user: %v", err)
	}

	o.addOrgMember(t, outsider)
	if _, err = o.owner.clients.Workspace.CreateMember(t.Context(), request); err != nil {
		t.Fatalf("grant to org member: %v", err)
	}
}

func TestWorkspaceOnlyMemberSeesOnlyTheirWorkspace(t *testing.T) {
	o := newMembershipOrg(t)
	member := o.api.signUp(t, "member")
	o.addOrgMember(t, member)
	ctx := t.Context()

	listed, err := member.clients.Workspace.ListOrgWorkspaces(
		ctx,
		&workspacev1.ListOrgWorkspacesRequest{OrgId: o.orgID},
	)
	if err != nil || len(listed.GetWorkspaces()) != 0 {
		t.Fatalf("member without grants listed %v (%v)", workspaceNames(listed.GetWorkspaces()), err)
	}

	o.grantWorkspace(t, member, o.shared)

	if ids := userOrgIDs(t, member); !slices.Equal(ids, []string{o.orgID}) {
		t.Fatalf("member orgs = %v, want [%s]", ids, o.orgID)
	}
	listed, err = member.clients.Workspace.ListOrgWorkspaces(ctx, &workspacev1.ListOrgWorkspacesRequest{OrgId: o.orgID})
	if err != nil {
		t.Fatalf("member list org workspaces: %v", err)
	}
	if got := workspaceNames(listed.GetWorkspaces()); !slices.Equal(got, []string{membershipSharedPath}) {
		t.Fatalf("member org workspaces = %v", got)
	}
	mine, err := member.clients.Workspace.ListUserWorkspaces(ctx, &workspacev1.ListUserWorkspacesRequest{})
	if err != nil {
		t.Fatalf("member list own workspaces: %v", err)
	}
	if got := workspaceNames(mine.GetWorkspaces()); !slices.Equal(got, []string{membershipSharedPath}) {
		t.Fatalf("member workspaces = %v", got)
	}
	shared, err := member.clients.Workspace.GetWorkspace(ctx, &workspacev1.GetWorkspaceRequest{WorkspaceId: o.shared})
	if err != nil || shared.GetWorkspace().GetOrgName() != membershipOrgName {
		t.Fatalf("member get shared = %+v (%v)", shared.GetWorkspace(), err)
	}

	_, err = member.clients.Workspace.GetWorkspace(ctx, &workspacev1.GetWorkspaceRequest{WorkspaceId: o.sibling})
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("member read sibling workspace: %v", err)
	}
	_, err = member.clients.Resource.ListWorkspaceResources(ctx, &resourcev1.ListWorkspaceResourcesRequest{
		WorkspaceId: o.sibling,
	})
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("member listed sibling resources: %v", err)
	}
}

func TestOrgReaderSeesEveryWorkspaceInTheOrg(t *testing.T) {
	o := newMembershipOrg(t)
	_, err := o.api.pool.Exec(t.Context(), `
INSERT INTO workspaces (org_id, name, created_by) VALUES ($1, 'ungranted', $2)`,
		uuid.MustParse(o.orgID), o.owner.id)
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	want := []string{membershipSharedPath, membershipOrgName + "/sibling", membershipOrgName + "/ungranted"}

	listed, err := o.owner.clients.Workspace.ListOrgWorkspaces(t.Context(), &workspacev1.ListOrgWorkspacesRequest{
		OrgId: o.orgID,
	})
	if err != nil {
		t.Fatalf("owner list org workspaces: %v", err)
	}
	if got := workspaceNames(listed.GetWorkspaces()); !slices.Equal(got, want) {
		t.Fatalf("owner org workspaces = %v", got)
	}
	mine, err := o.owner.clients.Workspace.ListUserWorkspaces(t.Context(), &workspacev1.ListUserWorkspacesRequest{})
	if err != nil {
		t.Fatalf("owner list own workspaces: %v", err)
	}
	if got := workspaceNames(mine.GetWorkspaces()); !slices.Equal(got, want) {
		t.Fatalf("owner workspaces = %v", got)
	}
}
