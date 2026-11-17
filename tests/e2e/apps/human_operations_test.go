package apps

import (
	"testing"

	"connectrpc.com/connect/v2"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestHumanAdministrationRequiresSession(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	me, err := p.User.User.WhoAmI(t.Context(), &userv1.WhoAmIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	userID := me.GetUser().GetId()
	orgAdmin := &tokenv1.EntityScope{
		EntityType: tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION, EntityId: w.OrgID, Scope: tokenv1.Scope_SCOPE_ADMIN,
	}
	personal := lifecycleToken(t, p.User, "human-admin", tokenv1.EntityType_ENTITY_TYPE_USER, userID,
		orgAdmin, &tokenv1.EntityScope{
			EntityType: tokenv1.EntityType_ENTITY_TYPE_USER, EntityId: userID, Scope: tokenv1.Scope_SCOPE_WRITE,
		})
	owned := lifecycleToken(t, p.User, "org-admin", tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION, w.OrgID, orgAdmin)
	for name, token := range map[string]string{"personal": personal, "organization": owned} {
		t.Run(name, func(t *testing.T) {
			client := p.Clients(token)
			for _, call := range []func() error{
				func() error {
					_, err := client.Org.CreateOrg(t.Context(), &orgv1.CreateOrgRequest{Name: new("token-organization")})
					return err
				},
				func() error {
					_, err := client.Org.InviteOrgMember(t.Context(), &orgv1.InviteOrgMemberRequest{
						OrgId: w.OrgID,
						Email: "token-invite@test.local",
					})
					return err
				},
				func() error {
					_, err := client.Org.SetOrgMemberRole(t.Context(), &orgv1.SetOrgMemberRoleRequest{
						OrgId:  w.OrgID,
						UserId: userID,
						Role:   orgv1.OrgRole_ORG_ROLE_ADMIN,
					})
					return err
				},
				func() error {
					_, err := client.Workspace.CreateMember(t.Context(), &workspacev1.CreateMemberRequest{
						WorkspaceId: w.ID,
						UserId:      userID,
						Scopes:      []string{"read"},
					})
					return err
				},
			} {
				if err := call(); connect.CodeOf(err) != connect.CodePermissionDenied {
					t.Fatalf("human administration with %s token = %v, want PermissionDenied", name, err)
				}
			}
		})
	}
}
