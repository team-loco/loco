package apps

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/internal/rpc"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const operationsEmail = "human-operations@test.local"

func TestOrganizationAdministrationUsesTokenPermissions(t *testing.T) {
	t.Parallel()
	u := signInLifecycleUser(t, operationsEmail)
	if !t.Run("personal tokens create organizations", func(t *testing.T) { tokenOrganizationCreation(t, u) }) {
		return
	}
	w := p.NewTenant(t)
	me, err := p.User.User.WhoAmI(t.Context(), &userv1.WhoAmIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	issuerID := me.GetUser().GetId()
	adminScopes := platform.Scopes(tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION, w.OrgID, everyScope...)
	personal := platform.CreateToken(
		t,
		p.User,
		"organization-admin",
		tokenv1.EntityType_ENTITY_TYPE_USER,
		issuerID,
		adminScopes...)
	owned := platform.CreateToken(
		t,
		p.User,
		"organization-admin",
		tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION,
		w.OrgID,
		adminScopes...)
	for _, actor := range []struct {
		name, credential string
		owner            tokenv1.EntityType
		ownerID          string
	}{
		{"personal", personal, tokenv1.EntityType_ENTITY_TYPE_USER, issuerID},
		{"organization", owned, tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION, w.OrgID},
	} {
		if !t.Run(actor.name, func(t *testing.T) {
			narrowed := platform.CreateToken(
				t,
				p.User,
				actor.name+"-organization-writer",
				actor.owner,
				actor.ownerID,
				platform.Scopes(
					tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION,
					w.OrgID,
					tokenv1.Scope_SCOPE_READ,
					tokenv1.Scope_SCOPE_WRITE,
				)...,
			)
			tokenMemberAdministration(t, w, u, p.Clients(actor.credential), p.Clients(narrowed))
		}) {
			return
		}
	}
	automation := p.Clients(owned)
	// An organization token has no human identity with which to own a new organization.
	if _, err = automation.Org.CreateOrg(
		t.Context(),
		&orgv1.CreateOrgRequest{Name: new("automation-organization")},
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("organization token creating a user-owned organization = %v, want PermissionDenied", err)
	}
	if _, err = automation.Org.ListUserOrgs(
		t.Context(),
		&orgv1.ListUserOrgsRequest{UserId: issuerID},
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("organization token listing its historical issuer's organizations = %v, want PermissionDenied", err)
	}
	u = inviteLifecycleUser(t, automation, w.OrgID, operationsEmail)
	setLifecycleRole(t, automation, w.OrgID, u.id, orgv1.OrgRole_ORG_ROLE_ADMIN)
	setLifecycleStatus(t, u.id, userv1.UserStatus_USER_STATUS_SUSPENDED)
	for _, call := range []func() error{
		func() error {
			_, callErr := automation.Org.SetOrgMemberRole(
				t.Context(),
				&orgv1.SetOrgMemberRoleRequest{
					OrgId:  w.OrgID,
					UserId: issuerID,
					Role:   orgv1.OrgRole_ORG_ROLE_MEMBER,
				},
			)
			return callErr
		},
		func() error {
			_, callErr := automation.Org.RemoveOrgMember(
				t.Context(),
				&orgv1.RemoveOrgMemberRequest{
					OrgId:  w.OrgID,
					UserId: issuerID,
				},
			)
			return callErr
		},
	} {
		if err = call(); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("handover to suspended admin = %v, want FailedPrecondition", err)
		}
	}
	setLifecycleStatus(t, u.id, userv1.UserStatus_USER_STATUS_ACTIVE)
	if _, err = automation.Org.RemoveOrgMember(
		t.Context(),
		&orgv1.RemoveOrgMemberRequest{OrgId: w.OrgID, UserId: issuerID},
	); err != nil {
		t.Fatal(err)
	}
	readLifecycleWorkspace(t, p.Clients(personal), w.ID, new(connect.CodePermissionDenied))
	readLifecycleWorkspace(t, automation, w.ID, nil)
	if _, err = automation.Org.InviteOrgMember(
		t.Context(),
		&orgv1.InviteOrgMemberRequest{OrgId: w.OrgID, Email: "after-handover@test.local"},
	); err != nil {
		t.Fatalf("owned administration after its issuer leaves = %v", err)
	}
}

func tokenOrganizationCreation(t *testing.T, u lifecycleUser) {
	t.Helper()
	read := &tokenv1.EntityScope{
		EntityType: tokenv1.EntityType_ENTITY_TYPE_USER,
		EntityId:   u.id,
		Scope:      tokenv1.Scope_SCOPE_READ,
	}
	writer := &tokenv1.EntityScope{
		EntityType: tokenv1.EntityType_ENTITY_TYPE_USER,
		EntityId:   u.id,
		Scope:      tokenv1.Scope_SCOPE_WRITE,
	}
	readOnly := p.Clients(
		platform.CreateToken(t, u.session, "self-reader", tokenv1.EntityType_ENTITY_TYPE_USER, u.id, read),
	)
	client := p.Clients(
		platform.CreateToken(t, u.session, "self-writer", tokenv1.EntityType_ENTITY_TYPE_USER, u.id, read, writer),
	)
	created, err := client.Org.CreateOrg(t.Context(), &orgv1.CreateOrgRequest{Name: new("personal-token-organization")})
	if err != nil {
		t.Fatal(err)
	}
	organization, err := u.session.Org.GetOrg(t.Context(), &orgv1.GetOrgRequest{OrgId: created.GetOrgId()})
	if err != nil {
		t.Fatal(err)
	}
	if organization.GetOrganization().GetCreatedBy() != u.id {
		t.Fatal("personal token created an organization for another identity")
	}
	if _, err = readOnly.Org.CreateOrg(
		t.Context(),
		&orgv1.CreateOrgRequest{Name: new("reader-organization")},
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("read-only organization creation = %v, want PermissionDenied", err)
	}
}

func tokenMemberAdministration(t *testing.T, w *platform.Workspace, u lifecycleUser, admin, narrow *rpc.Clients) {
	t.Helper()
	invitation, err := admin.Org.InviteOrgMember(
		t.Context(),
		&orgv1.InviteOrgMemberRequest{OrgId: w.OrgID, Email: operationsEmail},
	)
	if err != nil {
		t.Fatal(err)
	}
	invitationID := invitation.GetInvitation().GetId()
	if actorID := invitation.GetInvitation().GetInvitedBy().GetId(); actorID == "" || actorID == uuid.Nil.String() {
		t.Fatal("invitation omitted the authenticated actor's attribution")
	}
	for _, call := range []func() error{
		func() error {
			_, callErr := narrow.Org.InviteOrgMember(
				t.Context(),
				&orgv1.InviteOrgMemberRequest{
					OrgId: w.OrgID,
					Email: operationsEmail,
				},
			)
			return callErr
		},
		func() error {
			_, callErr := narrow.Workspace.CreateMember(
				t.Context(),
				&workspacev1.CreateMemberRequest{
					WorkspaceId: w.ID,
					UserId:      u.id,
					Scopes:      []string{"admin"},
				},
			)
			return callErr
		},
	} {
		if err = call(); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("non-admin token administration = %v, want PermissionDenied", err)
		}
	}
	if _, err = admin.Org.ResendInvitation(t.Context(), &orgv1.ResendInvitationRequest{Id: invitationID}); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Org.RevokeInvitation(t.Context(), &orgv1.RevokeInvitationRequest{Id: invitationID}); err != nil {
		t.Fatal(err)
	}
	invitations, err := admin.Org.ListInvitations(t.Context(), &orgv1.ListInvitationsRequest{OrgId: w.OrgID})
	if err != nil {
		t.Fatal(err)
	}
	if len(invitations.GetInvitations()) != 0 {
		t.Fatal("revoked invitation is still open")
	}
	claimed := inviteLifecycleUser(t, admin, w.OrgID, operationsEmail)
	if claimed.id != u.id {
		t.Fatal("invitation created another human identity")
	}
	readLifecycleWorkspace(t, claimed.session, w.ID, new(connect.CodePermissionDenied))
	setLifecycleRole(t, admin, w.OrgID, u.id, orgv1.OrgRole_ORG_ROLE_ADMIN)
	readLifecycleWorkspace(t, claimed.session, w.ID, nil)
	setLifecycleRole(t, admin, w.OrgID, u.id, orgv1.OrgRole_ORG_ROLE_MEMBER)
	readLifecycleWorkspace(t, claimed.session, w.ID, new(connect.CodePermissionDenied))
	// Workspace writers may delegate only permissions they hold, including ordinary membership.
	if _, err = narrow.Workspace.CreateMember(
		t.Context(),
		&workspacev1.CreateMemberRequest{WorkspaceId: w.ID, UserId: u.id, Scopes: []string{"read", "write"}},
	); err != nil {
		t.Fatal(err)
	}
	readLifecycleWorkspace(t, claimed.session, w.ID, nil)
	if _, err = admin.Workspace.CreateMember(
		t.Context(),
		&workspacev1.CreateMemberRequest{WorkspaceId: w.ID, UserId: u.id, Scopes: []string{"read", "write", "admin"}},
	); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Workspace.DeleteMember(
		t.Context(),
		&workspacev1.DeleteMemberRequest{WorkspaceId: w.ID, UserId: u.id},
	); err != nil {
		t.Fatal(err)
	}
	readLifecycleWorkspace(t, claimed.session, w.ID, new(connect.CodePermissionDenied))
	if _, err = admin.Org.RemoveOrgMember(
		t.Context(),
		&orgv1.RemoveOrgMemberRequest{OrgId: w.OrgID, UserId: u.id},
	); err != nil {
		t.Fatal(err)
	}
	members, err := admin.Org.ListOrgMembers(t.Context(), &orgv1.ListOrgMembersRequest{OrgId: w.OrgID})
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range members.GetMembers() {
		if member.GetUser().GetId() == u.id {
			t.Fatal("removed human is still a member")
		}
	}
}
