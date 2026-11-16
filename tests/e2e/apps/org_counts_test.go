package apps

import (
	"testing"

	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

func TestOrganizationCounts(t *testing.T) {
	t.Parallel()
	tenant := p.NewTenant(t)
	tenant.NewWorkspace(t, "second")
	token := ""
	for {
		response, err := p.User.Org.ListUserOrgs(
			t.Context(),
			&orgv1.ListUserOrgsRequest{UserId: p.UserID, PageSize: 1, PageToken: token},
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, org := range response.GetOrgs() {
			if org.GetId() == tenant.OrgID {
				if org.GetWorkspaceCount() != 2 || org.GetMemberCount() != 1 {
					t.Fatalf("counts = %d/%d, want 2/1", org.GetWorkspaceCount(), org.GetMemberCount())
				}
				return
			}
		}
		token = response.GetNextPageToken()
		if token == "" {
			t.Fatal("organization missing from list")
		}
	}
}
