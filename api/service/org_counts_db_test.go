package service

import (
	"testing"

	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

func TestListUserOrgsCountsAcrossPages(t *testing.T) {
	t.Parallel()
	o := newMembershipOrg(t)
	member := o.api.signUp(t, "member")
	o.addOrgMember(t, member)
	o.grantWorkspace(t, member, o.shared)
	empty, err := o.owner.clients.Org.CreateOrg(t.Context(), &orgv1.CreateOrgRequest{Name: new("empty")})
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	token := ""
	for {
		response, listErr := o.owner.clients.Org.ListUserOrgs(t.Context(), &orgv1.ListUserOrgsRequest{
			UserId: o.owner.id.String(), PageSize: 1, PageToken: token,
		})
		if listErr != nil {
			t.Fatal(listErr)
		}
		for _, org := range response.GetOrgs() {
			if seen[org.GetId()] {
				t.Fatalf("duplicate organization %s", org.GetId())
			}
			seen[org.GetId()] = true
			var workspaces, members int64
			switch org.GetId() {
			case o.orgID:
				workspaces, members = 2, 2
			case empty.GetOrgId():
				workspaces, members = 0, 1
			default:
				t.Fatalf("unexpected organization %s", org.GetId())
			}
			if org.WorkspaceCount == nil || org.MemberCount == nil || org.GetWorkspaceCount() != workspaces ||
				org.GetMemberCount() != members {
				t.Errorf(
					"organization %s: counts = %v/%v, want %d/%d",
					org.GetId(),
					org.WorkspaceCount,
					org.MemberCount,
					workspaces,
					members,
				)
			}
		}
		token = response.GetNextPageToken()
		if token == "" {
			break
		}
	}
	if len(seen) != 2 {
		t.Fatalf("listed %d organizations, want 2", len(seen))
	}
}
