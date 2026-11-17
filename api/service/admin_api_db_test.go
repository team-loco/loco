package service

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/team-loco/loco/api/authz"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	adminv1 "github.com/team-loco/loco/gen/go/loco/admin/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/internal/rpc"
)

func administrativeToken(t *testing.T, api *membershipAPI, issuer membershipUser, owner genDb.Entity,
	scopes ...genDb.EntityScope,
) (*rpc.Clients, reqctx.Principal) {
	t.Helper()
	requested := make([]*tokenv1.EntityScope, len(scopes))
	for i, scope := range scopes {
		requested[i] = &tokenv1.EntityScope{EntityType: dbEntityTypeToProto(scope.EntityType),
			EntityId: scope.EntityID.String(), Scope: dbScopeToProto(scope.Scope)}
	}
	issued, err := issuer.clients.Token.CreateToken(t.Context(), &tokenv1.CreateTokenRequest{
		Name: uuid.NewString(), EntityType: dbEntityTypeToProto(owner.Type), EntityId: owner.ID.String(),
		Scopes: requested, ExpiresInSec: 3600,
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := api.machine.GetToken(t.Context(), issued.GetToken())
	if err != nil {
		t.Fatal(err)
	}
	return rpc.ForHost(api.url, issued.GetToken()), principal
}

func organizationAdminScopes(id uuid.UUID) []genDb.EntityScope {
	scopes := make([]genDb.EntityScope, 0, 3)
	for _, scope := range []genDb.Scope{genDb.ScopeRead, genDb.ScopeWrite, genDb.ScopeAdmin} {
		scopes = append(scopes, genDb.EntityScope{EntityType: genDb.EntityTypeOrganization, EntityID: id, Scope: scope})
	}
	return scopes
}

func TestAPITokensAdministerInvitations(t *testing.T) {
	t.Parallel()
	for _, ownerType := range []genDb.EntityType{genDb.EntityTypeUser, genDb.EntityTypeOrganization} {
		t.Run(string(ownerType), func(t *testing.T) {
			o := newInvitationOrg(t)
			orgID := uuid.MustParse(o.orgID)
			owner := genDb.Entity{Type: ownerType, ID: orgID}
			if ownerType == genDb.EntityTypeUser {
				owner.ID = o.owner.id
			}
			client, principal := administrativeToken(t, o.api, o.owner, owner, organizationAdminScopes(orgID)...)
			invited, err := client.Org.InviteOrgMember(t.Context(), &orgv1.InviteOrgMemberRequest{
				OrgId: o.orgID, Email: "pending@delegation.test",
			})
			if err != nil {
				t.Fatal(err)
			}
			invitation := invited.GetInvitation()
			expectedInviter := o.owner.id
			if ownerType != genDb.EntityTypeUser {
				expectedInviter = principal.ActorID
			}
			if got := invitation.GetInvitedBy(); got.GetId() != expectedInviter.String() {
				t.Fatalf("inviter = %v, want %s", got, expectedInviter)
			}
			if ownerType != genDb.EntityTypeUser &&
				(invitation.GetInvitedBy().GetName() != invitationUnavailableName || invitation.GetInvitedBy().GetEmail() != "") {
				t.Fatalf("owned token has a human inviter profile: %v", invitation.GetInvitedBy())
			}
			var actorType string
			var actorID uuid.UUID
			var raw []byte
			if err = o.api.pool.QueryRow(
				t.Context(),
				"SELECT actor_type, actor_id, data FROM events WHERE type=$1 AND subject_id=$2",
				events.InvitationCreated, uuid.MustParse(invitation.GetId())).
				Scan(&actorType, &actorID, &raw); err != nil {
				t.Fatal(err)
			}
			if actorType != string(genDb.DeploymentActorTypeApiToken) || actorID != principal.ActorID {
				t.Fatalf("invitation actor = %s/%s", actorType, actorID)
			}
			var data map[string]string
			if err = json.Unmarshal(raw, &data); err != nil {
				t.Fatal(err)
			}
			if ownerType != genDb.EntityTypeUser &&
				(data[events.FieldInviterName] != invitationUnavailableName || data[events.FieldInviterEmail] != "") {
				t.Fatalf("owned invitation event = %v", data)
			}
			listed, err := client.Org.ListInvitations(t.Context(), &orgv1.ListInvitationsRequest{OrgId: o.orgID})
			if err != nil || len(listed.GetInvitations()) != 1 {
				t.Fatalf("list invitations = %v, %v", listed, err)
			}
			if _, err = client.Org.ResendInvitation(
				t.Context(),
				&orgv1.ResendInvitationRequest{Id: invitation.GetId()},
			); err != nil {
				t.Fatal(err)
			}
			if _, err = client.Org.RevokeInvitation(
				t.Context(),
				&orgv1.RevokeInvitationRequest{Id: invitation.GetId()},
			); err != nil {
				t.Fatal(err)
			}
			if _, err = client.Org.InviteOrgMember(
				t.Context(),
				&orgv1.InviteOrgMemberRequest{OrgId: o.orgID, Email: membershipEmail("claimed")},
			); err != nil {
				t.Fatal(err)
			}
			_, claimed := o.api.signIn(t, "claimed")
			if len(claimed) != 1 || claimed[0].OrgID != orgID {
				t.Fatalf("claim = %v", claimed)
			}
			if ownerType != genDb.EntityTypeUser && claimed[0].InviterName != invitationUnavailableName {
				t.Fatalf("owned inviter claim = %v", claimed)
			}
		})
	}
}

func TestAPITokensAdministerOrganizationAndWorkspace(t *testing.T) {
	t.Parallel()
	for _, ownerType := range []genDb.EntityType{genDb.EntityTypeUser, genDb.EntityTypeOrganization} {
		t.Run(string(ownerType), func(t *testing.T) {
			o := newInvitationOrg(t)
			member := o.api.signUp(t, "member")
			o.addOrgMember(t, member)
			orgID := uuid.MustParse(o.orgID)
			owner := genDb.Entity{Type: ownerType, ID: orgID}
			if ownerType == genDb.EntityTypeUser {
				owner.ID = o.owner.id
			}
			client, _ := administrativeToken(t, o.api, o.owner, owner, organizationAdminScopes(orgID)...)
			if _, err := client.Workspace.CreateMember(t.Context(), &workspacev1.CreateMemberRequest{
				WorkspaceId: o.shared, UserId: member.id.String(), Scopes: []string{genDb.ScopeRead, genDb.ScopeWrite},
			}); err != nil {
				t.Fatal(err)
			}
			scopes, err := o.api.queries.GetUserScopesOnEntity(
				t.Context(),
				genDb.GetUserScopesOnEntityParams{
					UserID:     member.id,
					EntityType: genDb.EntityTypeWorkspace,
					EntityID:   uuid.MustParse(o.shared),
				},
			)
			if err != nil || len(scopes) != 2 {
				t.Fatalf("workspace scopes = %v, %v", scopes, err)
			}
			if _, err = client.Workspace.DeleteMember(
				t.Context(),
				&workspacev1.DeleteMemberRequest{WorkspaceId: o.shared, UserId: member.id.String()},
			); err != nil {
				t.Fatal(err)
			}
			for _, role := range []orgv1.OrgRole{orgv1.OrgRole_ORG_ROLE_ADMIN, orgv1.OrgRole_ORG_ROLE_MEMBER} {
				if _, err = client.Org.SetOrgMemberRole(
					t.Context(),
					&orgv1.SetOrgMemberRoleRequest{OrgId: o.orgID, UserId: member.id.String(), Role: role},
				); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = client.Org.RemoveOrgMember(
				t.Context(),
				&orgv1.RemoveOrgMemberRequest{OrgId: o.orgID, UserId: member.id.String()},
			); err != nil {
				t.Fatal(err)
			}
			remains, err := o.api.queries.IsOrgMember(
				t.Context(),
				genDb.IsOrgMemberParams{OrgID: orgID, UserID: member.id},
			)
			if err != nil || remains {
				t.Fatalf("removed member still present = %v, %v", remains, err)
			}
		})
	}
}

func TestPersonalAPITokenCreatesHumanOwnedOrganizations(t *testing.T) {
	t.Parallel()
	api := newMembershipAPI(t)
	user := api.signUp(t, "creator")
	owner := genDb.Entity{Type: genDb.EntityTypeUser, ID: user.id}
	client, _ := administrativeToken(t, api, user, owner,
		genDb.EntityScope{EntityType: genDb.EntityTypeUser, EntityID: user.id, Scope: genDb.ScopeRead},
		genDb.EntityScope{EntityType: genDb.EntityTypeUser, EntityID: user.id, Scope: genDb.ScopeWrite},
	)
	first, err := client.Org.ListUserOrgs(
		t.Context(),
		&orgv1.ListUserOrgsRequest{UserId: user.id.String(), CreateIfMissing: true},
	)
	if err != nil || len(first.GetOrgs()) != 1 {
		t.Fatalf("first organization = %v, %v", first, err)
	}
	created, err := client.Org.CreateOrg(t.Context(), &orgv1.CreateOrgRequest{Name: new("delegated organization")})
	if err != nil {
		t.Fatal(err)
	}
	row, err := api.queries.GetOrgByID(t.Context(), uuid.MustParse(created.GetOrgId()))
	if err != nil || row.CreatedBy != user.id {
		t.Fatalf("organization owner = %v, %v", row, err)
	}
}

func TestPersonalAPITokenManagesSystemAdministratorsAndStatus(t *testing.T) {
	t.Parallel()
	api, admin := newSystemAdminAPI(t)
	member := api.signUp(t, "member")
	client, _ := administrativeToken(t, api, admin, genDb.Entity{Type: genDb.EntityTypeUser, ID: admin.id},
		genDb.EntityScope{EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin},
	)
	if _, err := client.SystemAdmin.GrantSystemAdmin(
		t.Context(),
		&adminv1.GrantSystemAdminRequest{UserId: member.id.String()},
	); err != nil {
		t.Fatal(err)
	}
	if got := adminSources(listAdmins(t, admin), member.id); len(got) != 1 {
		t.Fatalf("granted admin sources = %v", got)
	}
	if _, err := client.SystemAdmin.RevokeSystemAdmin(
		t.Context(),
		&adminv1.RevokeSystemAdminRequest{UserId: member.id.String()},
	); err != nil {
		t.Fatal(err)
	}
	for _, status := range []userv1.UserStatus{
		userv1.UserStatus_USER_STATUS_SUSPENDED,
		userv1.UserStatus_USER_STATUS_ACTIVE,
	} {
		if _, err := client.User.SetUserStatus(
			t.Context(),
			&userv1.SetUserStatusRequest{
				Key:    &userv1.SetUserStatusRequest_UserId{UserId: member.id.String()},
				Status: status,
			},
		); err != nil {
			t.Fatal(err)
		}
		row, err := api.queries.GetUserByID(t.Context(), member.id)
		if err != nil || (row.Status == genDb.UserStatusActive) != (status == userv1.UserStatus_USER_STATUS_ACTIVE) {
			t.Fatalf("user status = %v, %v", row, err)
		}
	}
}

func TestCreateTokenRejectsSystemOwner(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	p, _ := reqctx.PrincipalFrom(f.owner)
	p.Scopes = append(
		p.Scopes,
		genDb.EntityScope{EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin},
	)
	request := tokenRequest(genDb.Entity{Type: genDb.EntityTypeSystem, ID: uuid.Nil}, genDb.ScopeAdmin)
	request.Scopes = tokenRequest(
		genDb.Entity{Type: genDb.EntityTypeOrganization, ID: f.orgID},
		genDb.ScopeRead,
	).GetScopes()
	_, err := NewTokenServer(
		f.pool,
		f.queries,
		f.machine,
		limits.Defaults(),
	).CreateToken(reqctx.WithPrincipal(t.Context(), p), request)
	wantCode(t, err, connect.CodeInvalidArgument)
}

func TestAdministrativeAPITokensRequireActionPermissions(t *testing.T) {
	t.Parallel()
	for _, ownerType := range []genDb.EntityType{genDb.EntityTypeUser, genDb.EntityTypeOrganization} {
		t.Run(string(ownerType), func(t *testing.T) {
			o := newInvitationOrg(t)
			member := o.api.signUp(t, "member")
			o.addOrgMember(t, member)
			invitation := o.invite(t, "pending@permission.test")
			orgID := uuid.MustParse(o.orgID)
			owner := genDb.Entity{Type: ownerType, ID: orgID}
			if ownerType == genDb.EntityTypeUser {
				owner.ID = o.owner.id
			}
			if err := o.api.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
				UserID: o.owner.id, EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin,
			}); err != nil {
				t.Fatal(err)
			}

			client, _ := administrativeToken(
				t,
				o.api,
				o.owner,
				owner,
				genDb.EntityScope{EntityType: genDb.EntityTypeOrganization, EntityID: orgID, Scope: genDb.ScopeRead},
			)
			for _, tc := range []struct {
				name string
				call func() error
			}{
				{"invite member", func() error {
					_, err := client.Org.InviteOrgMember(t.Context(), &orgv1.InviteOrgMemberRequest{
						OrgId: o.orgID,
						Email: "denied@permission.test",
					})
					return err
				}},
				{"resend invitation", func() error {
					_, err := client.Org.ResendInvitation(t.Context(), &orgv1.ResendInvitationRequest{Id: invitation.GetId()})
					return err
				}},
				{"revoke invitation", func() error {
					_, err := client.Org.RevokeInvitation(t.Context(), &orgv1.RevokeInvitationRequest{Id: invitation.GetId()})
					return err
				}},
				{"org role", func() error {
					_, err := client.Org.SetOrgMemberRole(t.Context(), &orgv1.SetOrgMemberRoleRequest{
						OrgId:  o.orgID,
						UserId: member.id.String(),
						Role:   orgv1.OrgRole_ORG_ROLE_ADMIN,
					})
					return err
				}},
				{"org removal", func() error {
					_, err := client.Org.RemoveOrgMember(t.Context(), &orgv1.RemoveOrgMemberRequest{
						OrgId:  o.orgID,
						UserId: member.id.String(),
					})
					return err
				}},
				{"workspace grant", func() error {
					_, err := client.Workspace.CreateMember(t.Context(), &workspacev1.CreateMemberRequest{
						WorkspaceId: o.shared,
						UserId:      member.id.String(),
						Scopes:      []string{genDb.ScopeRead},
					})
					return err
				}},
				{"workspace removal", func() error {
					_, err := client.Workspace.DeleteMember(t.Context(), &workspacev1.DeleteMemberRequest{
						WorkspaceId: o.shared,
						UserId:      member.id.String(),
					})
					return err
				}},
				{"system grant", func() error {
					_, err := client.SystemAdmin.GrantSystemAdmin(t.Context(), &adminv1.GrantSystemAdminRequest{
						UserId: member.id.String(),
					})
					return err
				}},
				{"system revoke", func() error {
					_, err := client.SystemAdmin.RevokeSystemAdmin(t.Context(), &adminv1.RevokeSystemAdminRequest{
						UserId: member.id.String(),
					})
					return err
				}},
				{"user suspension", func() error {
					_, err := client.User.SetUserStatus(t.Context(), &userv1.SetUserStatusRequest{
						Key:    &userv1.SetUserStatusRequest_UserId{UserId: member.id.String()},
						Status: userv1.UserStatus_USER_STATUS_SUSPENDED,
					})
					return err
				}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if err := tc.call(); connect.CodeOf(err) != connect.CodePermissionDenied {
						t.Fatalf("error = %v, want PermissionDenied", err)
					}
				})
			}
			remains, err := o.api.queries.IsOrgMember(
				t.Context(),
				genDb.IsOrgMemberParams{OrgID: orgID, UserID: member.id},
			)
			if err != nil || !remains {
				t.Fatalf("denied writes changed membership: %v, %v", remains, err)
			}
			// Administration alone does not include the read permission being granted.
			adminOnly, _ := administrativeToken(
				t,
				o.api,
				o.owner,
				owner,
				genDb.EntityScope{
					EntityType: genDb.EntityTypeWorkspace,
					EntityID:   uuid.MustParse(o.shared),
					Scope:      genDb.ScopeAdmin,
				},
			)
			_, err = adminOnly.Workspace.CreateMember(
				t.Context(),
				&workspacev1.CreateMemberRequest{
					WorkspaceId: o.shared,
					UserId:      member.id.String(),
					Scopes:      []string{genDb.ScopeRead},
				},
			)
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("unheld permission grant = %v", err)
			}
		})
	}
}

func TestPersonalAPITokenAdministrationUsesLiveUserAccess(t *testing.T) {
	t.Parallel()
	o := newInvitationOrg(t)
	orgID := uuid.MustParse(o.orgID)
	client, _ := administrativeToken(
		t,
		o.api,
		o.owner,
		genDb.Entity{Type: genDb.EntityTypeUser, ID: o.owner.id},
		organizationAdminScopes(orgID)...)
	if err := o.api.queries.RemoveUserScope(t.Context(), genDb.RemoveUserScopeParams{
		UserID: o.owner.id, EntityType: genDb.EntityTypeOrganization, EntityID: orgID, Scope: genDb.ScopeAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := client.Org.InviteOrgMember(
		t.Context(),
		&orgv1.InviteOrgMemberRequest{OrgId: o.orgID, Email: "lost-access@permission.test"},
	)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("removed live access = %v", err)
	}
	if _, err = o.api.queries.SetUserStatus(
		t.Context(),
		genDb.SetUserStatusParams{ID: o.owner.id, Status: genDb.UserStatusSuspended},
	); err != nil {
		t.Fatal(err)
	}
	_, err = client.Org.InviteOrgMember(
		t.Context(),
		&orgv1.InviteOrgMemberRequest{OrgId: o.orgID, Email: "suspended@permission.test"},
	)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("suspended personal actor = %v", err)
	}
}

func TestOwnedAPITokenAdministrationKeepsAHumanSuccessor(t *testing.T) {
	t.Parallel()
	o := newInvitationOrg(t)
	successor := o.api.signUp(t, "successor")
	promoteLifecycleAdmin(t, o, successor)
	orgID := uuid.MustParse(o.orgID)
	client, principal := administrativeToken(
		t,
		o.api,
		o.owner,
		genDb.Entity{Type: genDb.EntityTypeOrganization, ID: orgID},
		organizationAdminScopes(orgID)...)
	if principal.UserID != uuid.Nil {
		t.Fatalf("owned token has human principal %s", principal.UserID)
	}
	if _, err := client.Org.CreateOrg(
		t.Context(),
		&orgv1.CreateOrgRequest{Name: new("no human owner")},
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("owned token creates human-owned org = %v", err)
	}
	if _, err := successor.clients.Org.RemoveOrgMember(
		t.Context(),
		&orgv1.RemoveOrgMemberRequest{OrgId: o.orgID, UserId: o.owner.id.String()},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Org.InviteOrgMember(
		t.Context(),
		&orgv1.InviteOrgMemberRequest{OrgId: o.orgID, Email: "after-departure@permission.test"},
	); err != nil {
		t.Fatalf("owned token depends on issuer membership: %v", err)
	}
	if _, err := client.Org.SetOrgMemberRole(
		t.Context(),
		&orgv1.SetOrgMemberRoleRequest{
			OrgId:  o.orgID,
			UserId: successor.id.String(),
			Role:   orgv1.OrgRole_ORG_ROLE_MEMBER,
		},
	); connect.CodeOf(
		err,
	) != connect.CodeFailedPrecondition {
		t.Fatalf("owned token replaces human admin = %v", err)
	}
	if _, err := client.Org.RemoveOrgMember(
		t.Context(),
		&orgv1.RemoveOrgMemberRequest{OrgId: o.orgID, UserId: successor.id.String()},
	); connect.CodeOf(
		err,
	) != connect.CodeFailedPrecondition {
		t.Fatalf("owned token removes last human admin = %v", err)
	}
}

func TestPersonalAPITokenOrganizationCreationRequiresWrite(t *testing.T) {
	t.Parallel()
	api := newMembershipAPI(t)
	user := api.signUp(t, "reader")
	client, _ := administrativeToken(t, api, user, genDb.Entity{Type: genDb.EntityTypeUser, ID: user.id},
		genDb.EntityScope{EntityType: genDb.EntityTypeUser, EntityID: user.id, Scope: genDb.ScopeRead},
	)
	if _, err := client.Org.CreateOrg(
		t.Context(),
		&orgv1.CreateOrgRequest{Name: new("unauthorized")},
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("read-only organization creation = %v", err)
	}
	if _, err := client.Org.ListUserOrgs(
		t.Context(),
		&orgv1.ListUserOrgsRequest{UserId: user.id.String(), CreateIfMissing: true},
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("read-only first organization = %v", err)
	}
	count, err := api.queries.CountOrgsCreatedBy(t.Context(), user.id)
	if err != nil || count != 0 {
		t.Fatalf("denied creation changed organizations: %d, %v", count, err)
	}
}

func TestWorkspaceCreationGrantsTheHumanBehindPersonalTokens(t *testing.T) {
	t.Parallel()
	for _, ownerType := range []genDb.EntityType{genDb.EntityTypeUser, genDb.EntityTypeOrganization} {
		t.Run(string(ownerType), func(t *testing.T) {
			o := newInvitationOrg(t)
			orgID := uuid.MustParse(o.orgID)
			owner := genDb.Entity{Type: ownerType, ID: orgID}
			if ownerType == genDb.EntityTypeUser {
				owner.ID = o.owner.id
			}
			client, principal := administrativeToken(t, o.api, o.owner, owner,
				genDb.EntityScope{EntityType: genDb.EntityTypeOrganization, EntityID: orgID, Scope: genDb.ScopeWrite})
			created, err := client.Workspace.CreateWorkspace(t.Context(), &workspacev1.CreateWorkspaceRequest{
				OrgId: o.orgID, Name: "token-created",
			})
			if err != nil {
				t.Fatal(err)
			}
			id := uuid.MustParse(created.GetWorkspaceId())
			workspace, err := o.api.queries.GetWorkspaceByIDQuery(t.Context(), id)
			if err != nil || workspace.CreatedBy != principal.ActorID {
				t.Fatalf("creator = %s, %v; want token actor %s", workspace.CreatedBy, err, principal.ActorID)
			}
			grants, err := authz.New(o.api.queries).UserScopes(t.Context(), o.owner.id)
			if err != nil {
				t.Fatal(err)
			}
			for _, scope := range []genDb.Scope{genDb.ScopeRead, genDb.ScopeWrite, genDb.ScopeAdmin} {
				grant := genDb.EntityScope{EntityType: genDb.EntityTypeWorkspace, EntityID: id, Scope: scope}
				if got, want := slices.Contains(grants, grant), ownerType == genDb.EntityTypeUser; got != want {
					t.Errorf("human creator %s grant = %t, want %t", scope, got, want)
				}
				permission, callErr := client.Token.CheckPermission(t.Context(), &tokenv1.CheckPermissionRequest{
					EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
					EntityId:   id.String(),
					Scope:      dbScopeToProto(scope),
				})
				if callErr != nil || permission.GetAllowed() != (scope == genDb.ScopeWrite) {
					t.Errorf("bounded token %s permission = %v, %v", scope, permission, callErr)
				}
			}
		})
	}
}
