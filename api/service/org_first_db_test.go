package service

import (
	"testing"

	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestListUserOrgsCreatesTheFirstOrgOnRequest(t *testing.T) {
	api := newMembershipAPI(t)
	user := api.signUp(t, "first.user+cli")
	ctx := t.Context()

	if ids := userOrgIDs(t, user); len(ids) != 0 {
		t.Fatalf("orgs without create_if_missing = %v, want none", ids)
	}

	req := &orgv1.ListUserOrgsRequest{UserId: user.id.String(), CreateIfMissing: true}
	for range 2 {
		resp, err := user.clients.Org.ListUserOrgs(ctx, req)
		if err != nil {
			t.Fatalf("list user orgs: %v", err)
		}
		orgs := resp.GetOrgs()
		if len(orgs) != 1 || orgs[0].GetName() != "first-user-cli-acme-test-org" {
			t.Fatalf("orgs = %v, want one named first-user-cli-acme-test-org", orgs)
		}
		workspaces, err := user.clients.Workspace.ListOrgWorkspaces(ctx, &workspacev1.ListOrgWorkspacesRequest{
			OrgId: orgs[0].GetId(),
		})
		if err != nil {
			t.Fatalf("list workspaces: %v", err)
		}
		if ws := workspaces.GetWorkspaces(); len(ws) != 1 || ws[0].GetName() != firstWorkspaceName {
			t.Fatalf("workspaces = %v, want one named %s", ws, firstWorkspaceName)
		}
		assertOrgAdmin(t, api, user, orgs[0].GetId())
	}
}

func assertOrgAdmin(t *testing.T, api *membershipAPI, user membershipUser, orgID string) {
	t.Helper()
	scopes, err := api.queries.GetUserScopesOnOrganization(t.Context(), genDb.GetUserScopesOnOrganizationParams{
		ID: uuid.MustParse(orgID), UserID: user.id,
	})
	if err != nil {
		t.Fatalf("org scopes: %v", err)
	}
	for _, scope := range scopes {
		if scope.EntityType == genDb.EntityTypeOrganization && scope.Scope == genDb.ScopeAdmin {
			return
		}
	}
	t.Fatalf("scopes on %s = %v, want organization admin", orgID, scopes)
}
