package apps

import (
	"testing"

	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

func TestOrganizationMemberDates(t *testing.T) {
	t.Parallel()
	tenant := p.NewTenant(t)
	response, err := p.User.Org.ListOrgMembers(t.Context(), &orgv1.ListOrgMembersRequest{OrgId: tenant.OrgID})
	if err != nil {
		t.Fatal(err)
	}
	members := response.GetMembers()
	if len(members) != 1 || members[0].GetUser().GetId() != p.UserID {
		t.Fatalf("members = %v", members)
	}
	if members[0].GetJoinedAt() == nil || members[0].GetLastLoginAt() == nil {
		t.Fatalf("member dates = %v", members[0])
	}
}
