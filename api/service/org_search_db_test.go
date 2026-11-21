package service

import (
	"testing"

	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

func TestListUserOrgsSearchesNames(t *testing.T) {
	t.Parallel()
	o := newMembershipOrg(t)
	empty, err := o.owner.clients.Org.CreateOrg(t.Context(), &orgv1.CreateOrgRequest{Name: new("Empty_Org")})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query, name string
		want        []string
	}{
		{"", "", []string{empty.GetOrgId(), o.orgID}},
		{"mpty", "", []string{empty.GetOrgId()}},
		{"Y_O", "", []string{empty.GetOrgId()}},
		{"y%o", "", nil},
		{"", "EMPTY_org", []string{empty.GetOrgId()}},
		{"", "mpty_org", nil},
	} {
		response, listErr := o.owner.clients.Org.ListUserOrgs(t.Context(), &orgv1.ListUserOrgsRequest{
			UserId: o.owner.id.String(), Query: tc.query, Name: tc.name, IncludeTotalCount: true,
		})
		if listErr != nil {
			t.Fatal(listErr)
		}
		var got []string
		for _, org := range response.GetOrgs() {
			got = append(got, org.GetId())
		}
		if len(got) != len(tc.want) || response.GetTotalCount() != int32(len(tc.want)) {
			t.Errorf("query %q name %q listed %v with total %d, want %v",
				tc.query, tc.name, got, response.GetTotalCount(), tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("query %q name %q listed %v, want %v", tc.query, tc.name, got, tc.want)
			}
		}
	}
}
