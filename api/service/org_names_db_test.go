package service

import (
	"testing"

	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestOrgsAndWorkspacesCanShareNames(t *testing.T) {
	api := newMembershipAPI(t)
	ctx := t.Context()
	for _, subject := range []string{"alice", "bob"} {
		user := api.signUp(t, subject)
		org, err := user.clients.Org.CreateOrg(ctx, &orgv1.CreateOrgRequest{Name: new(membershipOrgName)})
		if err != nil {
			t.Fatalf("%s creates org %s: %v", subject, membershipOrgName, err)
		}
		for range 2 {
			_, err = user.clients.Workspace.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{
				OrgId: org.GetOrgId(), Name: firstWorkspaceName,
			})
			if err != nil {
				t.Fatalf("%s creates workspace %s: %v", subject, firstWorkspaceName, err)
			}
		}
		other, err := user.clients.Workspace.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{
			OrgId: org.GetOrgId(), Name: "other",
		})
		if err != nil {
			t.Fatalf("create workspace: %v", err)
		}
		_, err = user.clients.Workspace.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{
			WorkspaceId: other.GetWorkspaceId(), Name: new(firstWorkspaceName),
		})
		if err != nil {
			t.Fatalf("rename workspace to %s: %v", firstWorkspaceName, err)
		}
	}

	owner := api.signUp(t, "carol")
	org, err := owner.clients.Org.CreateOrg(ctx, &orgv1.CreateOrgRequest{Name: new("other")})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	_, err = owner.clients.Org.UpdateOrg(ctx, &orgv1.UpdateOrgRequest{
		OrgId: org.GetOrgId(), Name: new(membershipOrgName),
	})
	if err != nil {
		t.Fatalf("rename org to %s: %v", membershipOrgName, err)
	}
}
