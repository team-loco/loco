package service

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	adminv1 "github.com/team-loco/loco/gen/go/loco/admin/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/internal/rpc"
)

func TestHumanMutationsRequireSessionOverRPC(t *testing.T) {
	t.Parallel()
	for _, ownerType := range []genDb.EntityType{genDb.EntityTypeUser, genDb.EntityTypeOrganization} {
		t.Run(string(ownerType), func(t *testing.T) {
			o := newMembershipOrg(t)
			member := o.api.signUp(t, "member")
			o.addOrgMember(t, member)
			o.grantWorkspace(t, member, o.shared)
			if err := o.api.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
				UserID: o.owner.id, EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin,
			}); err != nil {
				t.Fatal(err)
			}
			scopes := []genDb.EntityScope{
				{EntityType: genDb.EntityTypeOrganization, EntityID: uuid.MustParse(o.orgID), Scope: genDb.ScopeRead},
				{EntityType: genDb.EntityTypeOrganization, EntityID: uuid.MustParse(o.orgID), Scope: genDb.ScopeWrite},
				{EntityType: genDb.EntityTypeOrganization, EntityID: uuid.MustParse(o.orgID), Scope: genDb.ScopeAdmin},
			}
			owner := genDb.Entity{Type: ownerType, ID: uuid.MustParse(o.orgID)}
			if ownerType == genDb.EntityTypeUser {
				owner.ID = o.owner.id
				scopes = append(scopes,
					genDb.EntityScope{EntityType: genDb.EntityTypeUser, EntityID: o.owner.id, Scope: genDb.ScopeWrite},
					genDb.EntityScope{EntityType: genDb.EntityTypeUser, EntityID: o.owner.id, Scope: genDb.ScopeRead},
					genDb.EntityScope{EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin},
				)
			}
			o.api.machine.Cfg.MaxAPITokenDuration = membershipSessionDuration
			token, _, err := o.api.machine.Issue(
				t.Context(), "human-operation-check", o.owner.id, owner, scopes, membershipSessionDuration,
			)
			if err != nil {
				t.Fatal(err)
			}
			client := rpc.ForHost(o.api.url, token)
			invitation, err := o.owner.clients.Org.InviteOrgMember(t.Context(), &orgv1.InviteOrgMemberRequest{
				OrgId: o.orgID, Email: "pending@session.test",
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, check := range []struct {
				name string
				call func() error
			}{
				{"create organization", func() error {
					_, err := client.Org.CreateOrg(t.Context(), &orgv1.CreateOrgRequest{Name: new("token-created")})
					return err
				}},
				{"first organization", func() error {
					_, err := client.Org.ListUserOrgs(t.Context(), &orgv1.ListUserOrgsRequest{
						UserId:          o.owner.id.String(),
						CreateIfMissing: true,
					})
					return err
				}},
				{"invite person", func() error {
					_, err := client.Org.InviteOrgMember(t.Context(), &orgv1.InviteOrgMemberRequest{
						OrgId: o.orgID,
						Email: "other@session.test",
					})
					return err
				}},
				{"resend invitation", func() error {
					_, err := client.Org.ResendInvitation(t.Context(), &orgv1.ResendInvitationRequest{
						Id: invitation.GetInvitation().GetId(),
					})
					return err
				}},
				{"revoke invitation", func() error {
					_, err := client.Org.RevokeInvitation(t.Context(), &orgv1.RevokeInvitationRequest{
						Id: invitation.GetInvitation().GetId(),
					})
					return err
				}},
				{"change org role", func() error {
					_, err := client.Org.SetOrgMemberRole(t.Context(), &orgv1.SetOrgMemberRoleRequest{
						OrgId:  o.orgID,
						UserId: member.id.String(),
						Role:   orgv1.OrgRole_ORG_ROLE_ADMIN,
					})
					return err
				}},
				{"remove org member", func() error {
					_, err := client.Org.RemoveOrgMember(t.Context(), &orgv1.RemoveOrgMemberRequest{
						OrgId:  o.orgID,
						UserId: member.id.String(),
					})
					return err
				}},
				{"grant workspace", func() error {
					_, err := client.Workspace.CreateMember(t.Context(), &workspacev1.CreateMemberRequest{
						WorkspaceId: o.shared,
						UserId:      member.id.String(),
						Scopes:      []string{"read"},
					})
					return err
				}},
				{"remove workspace member", func() error {
					_, err := client.Workspace.DeleteMember(t.Context(), &workspacev1.DeleteMemberRequest{
						WorkspaceId: o.shared,
						UserId:      member.id.String(),
					})
					return err
				}},
				{"grant system admin", func() error {
					_, err := client.SystemAdmin.GrantSystemAdmin(t.Context(), &adminv1.GrantSystemAdminRequest{
						UserId: member.id.String(),
					})
					return err
				}},
				{"revoke system admin", func() error {
					_, err := client.SystemAdmin.RevokeSystemAdmin(t.Context(), &adminv1.RevokeSystemAdminRequest{
						UserId: member.id.String(),
					})
					return err
				}},
				{"change human status", func() error {
					_, err := client.User.SetUserStatus(t.Context(), &userv1.SetUserStatusRequest{
						Key:    &userv1.SetUserStatusRequest_UserId{UserId: member.id.String()},
						Status: userv1.UserStatus_USER_STATUS_ACTIVE,
					})
					return err
				}},
			} {
				t.Run(check.name, func(t *testing.T) {
					if err := check.call(); connect.CodeOf(err) != connect.CodePermissionDenied {
						t.Fatalf("%s = %v, want PermissionDenied", check.name, err)
					}
				})
			}
		})
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
