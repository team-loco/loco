package service

import (
	"slices"
	"testing"

	"connectrpc.com/connect/v2"

	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

type orgListFixture struct {
	reader membershipUser
	// ids of the organizations by name: alpha and beta belong to dana, gamma to erin.
	alpha, beta, gamma string
}

// newOrgListFixture gives a system reader who belongs to no organization three organizations:
// alpha with a workspace and a service, beta, which is suspended, and gamma.
func newOrgListFixture(t *testing.T) (*membershipAPI, orgListFixture) {
	t.Helper()
	a, _ := newSystemRoleAPI(t)
	f := orgListFixture{reader: a.signUp(t, configReaderSubject)}
	dana := a.signUp(t, "dana")
	erin := a.signUp(t, "erin")
	create := func(user membershipUser, name string) string {
		created, err := user.clients.Org.CreateOrg(t.Context(), &orgv1.CreateOrgRequest{Name: new(name)})
		if err != nil {
			t.Fatalf("create org %s: %v", name, err)
		}
		return created.GetOrgId()
	}
	f.alpha, f.beta, f.gamma = create(dana, "Alpha"), create(dana, "Beta"), create(erin, "Gamma")
	o := &membershipOrg{api: a, owner: dana, orgID: f.alpha}
	o.shared = o.createWorkspace(t, "shared")
	o.sharedResource(t, "api")
	suspend := "UPDATE organizations SET suspended_at = now(), suspension_reason = 'unpaid' WHERE id = $1"
	if _, err := a.pool.Exec(t.Context(), suspend, f.beta); err != nil {
		t.Fatal(err)
	}
	return a, f
}

func listedOrgIDs(response *orgv1.ListOrgsResponse) []string {
	ids := make([]string, 0, len(response.GetOrgs()))
	for _, summary := range response.GetOrgs() {
		ids = append(ids, summary.GetOrganization().GetId())
	}
	return ids
}

func TestListOrgsShowsEveryOrganizationToASystemReader(t *testing.T) {
	t.Parallel()
	_, f := newOrgListFixture(t)
	response, err := f.reader.clients.Org.ListOrgs(t.Context(), &orgv1.ListOrgsRequest{
		IncludeTotalCount: true, OrderBy: orgv1.OrgOrder_ORG_ORDER_NAME,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's reader and admin sign-ins create no organization.
	if got, want := listedOrgIDs(response), []string{f.alpha, f.beta, f.gamma}; !slices.Equal(got, want) ||
		response.GetTotalCount() != 3 {
		t.Fatalf("listed %v with total %d, want %v", got, response.GetTotalCount(), want)
	}
	alpha := response.GetOrgs()[0]
	if alpha.GetOrganization().GetWorkspaceCount() != 1 || alpha.GetOrganization().GetMemberCount() != 1 ||
		alpha.GetResourceCount() != 1 || len(alpha.GetOwners()) != 1 ||
		alpha.GetOwners()[0].GetEmail() != membershipEmail("dana") || alpha.GetOrganization().GetCreatedAt() == nil {
		t.Fatalf("alpha = %v, want one workspace, member, resource and owner dana", alpha)
	}
	beta := response.GetOrgs()[1].GetOrganization()
	if beta.GetSuspendedAt() == nil || beta.GetSuspensionReason() != "unpaid" {
		t.Fatalf("beta = %v, want suspended for unpaid", beta)
	}
}

func TestListOrgsSearchesNameIDAndOwnerEmail(t *testing.T) {
	t.Parallel()
	_, f := newOrgListFixture(t)
	for query, want := range map[string][]string{
		"ALPH":                  {f.alpha},
		f.gamma:                 {f.gamma},
		membershipEmail("dana"): {f.beta, f.alpha},
		"erin@":                 {f.gamma},
		"nobody":                nil,
		"%":                     nil,
	} {
		response, err := f.reader.clients.Org.ListOrgs(t.Context(), &orgv1.ListOrgsRequest{
			Query: query, IncludeTotalCount: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := listedOrgIDs(response); !slices.Equal(got, want) || int(response.GetTotalCount()) != len(want) {
			t.Errorf("query %q listed %v with total %d, want %v", query, got, response.GetTotalCount(), want)
		}
	}
}

func TestListOrgsFiltersByState(t *testing.T) {
	t.Parallel()
	_, f := newOrgListFixture(t)
	for state, want := range map[orgv1.OrgState][]string{
		orgv1.OrgState_ORG_STATE_SUSPENDED: {f.beta},
		orgv1.OrgState_ORG_STATE_ACTIVE:    {f.gamma, f.alpha},
	} {
		response, err := f.reader.clients.Org.ListOrgs(t.Context(), &orgv1.ListOrgsRequest{
			State: state, IncludeTotalCount: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := listedOrgIDs(response); !slices.Equal(got, want) || int(response.GetTotalCount()) != len(want) {
			t.Errorf("state %v listed %v with total %d, want %v", state, got, response.GetTotalCount(), want)
		}
	}
}

func TestListOrgsPagesInEveryOrder(t *testing.T) {
	t.Parallel()
	_, f := newOrgListFixture(t)
	for _, tc := range []struct {
		name       string
		order      orgv1.OrgOrder
		descending bool
		want       []string
	}{
		{"newest first", orgv1.OrgOrder_ORG_ORDER_UNSPECIFIED, false, []string{f.gamma, f.beta, f.alpha}},
		{"oldest first", orgv1.OrgOrder_ORG_ORDER_CREATED_AT, false, []string{f.alpha, f.beta, f.gamma}},
		{"by name", orgv1.OrgOrder_ORG_ORDER_NAME, false, []string{f.alpha, f.beta, f.gamma}},
		{"name descending", orgv1.OrgOrder_ORG_ORDER_NAME, true, []string{f.gamma, f.beta, f.alpha}},
	} {
		var got []string
		token := ""
		for range 3 {
			response, err := f.reader.clients.Org.ListOrgs(t.Context(), &orgv1.ListOrgsRequest{
				PageSize: 1, PageToken: token, OrderBy: tc.order, Descending: tc.descending, IncludeTotalCount: true,
			})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if response.GetTotalCount() != 3 {
				t.Errorf("%s: total %d on a page of one, want 3", tc.name, response.GetTotalCount())
			}
			got = append(got, listedOrgIDs(response)...)
			if token = response.GetNextPageToken(); token == "" {
				break
			}
		}
		if !slices.Equal(got, tc.want) || token != "" {
			t.Errorf("%s: paged %v with token %q, want %v and no token", tc.name, got, token, tc.want)
		}
	}
}

func TestListOrgsRejectsATokenFromAnotherOrder(t *testing.T) {
	t.Parallel()
	_, f := newOrgListFixture(t)
	first, err := f.reader.clients.Org.ListOrgs(t.Context(), &orgv1.ListOrgsRequest{PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.reader.clients.Org.ListOrgs(t.Context(), &orgv1.ListOrgsRequest{
		PageSize: 1, PageToken: first.GetNextPageToken(), OrderBy: orgv1.OrgOrder_ORG_ORDER_NAME,
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

func TestListOrgsDeniesNonOperators(t *testing.T) {
	t.Parallel()
	a, _ := newOrgListFixture(t)
	member := a.signUp(t, "dana")
	_, err := member.clients.Org.ListOrgs(t.Context(), &orgv1.ListOrgsRequest{})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("organization owner: err = %v, want PermissionDenied", err)
	}
}
