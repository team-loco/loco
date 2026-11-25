package service

import (
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

type orgListFixture struct {
	reader membershipUser
	// ids of the organizations by name: alpha and beta belong to dana, gamma to erin.
	alpha, beta, gamma string
}

// newOrgListFixture gives a system reader who belongs to no organization three organizations:
// alpha with a workspace, a service, two owners (dana and fay) and one more member (gus), beta, which
// is suspended, and gamma.
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
	fay, gus := a.signUp(t, "fay"), a.signUp(t, "gus")
	for user, scope := range map[membershipUser]string{fay: genDb.ScopeAdmin, gus: genDb.ScopeMember} {
		if err := a.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
			UserID: user.id, EntityType: genDb.EntityTypeOrganization, EntityID: uuid.MustParse(f.alpha), Scope: scope,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for user, name := range map[membershipUser]string{dana: "Dana Day", fay: "Fay Fox"} {
		if _, err := a.pool.Exec(t.Context(), "UPDATE users SET name = $2 WHERE id = $1", user.id, name); err != nil {
			t.Fatal(err)
		}
	}
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
	owners := make([]string, 0, len(alpha.GetOwners()))
	for _, owner := range alpha.GetOwners() {
		owners = append(owners, owner.GetName()+" <"+owner.GetEmail()+">")
	}
	wantOwners := []string{"Dana Day <" + membershipEmail("dana") + ">", "Fay Fox <" + membershipEmail("fay") + ">"}
	if alpha.GetOrganization().GetWorkspaceCount() != 1 || alpha.GetOrganization().GetMemberCount() != 3 ||
		alpha.GetResourceCount() != 1 || !slices.Equal(owners, wantOwners) || alpha.GetOrganization().GetCreatedAt() == nil {
		t.Fatalf("alpha = %v, want one workspace, three members, one resource and owners %v", alpha, wantOwners)
	}
	gamma := response.GetOrgs()[2]
	if gamma.GetOrganization().GetWorkspaceCount() != 0 || gamma.GetOrganization().GetMemberCount() != 1 ||
		gamma.GetResourceCount() != 0 || len(gamma.GetOwners()) != 1 {
		t.Fatalf("gamma = %v, want no workspace or resource and one member who owns it", gamma)
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
		membershipEmail("fay"):  {f.alpha},
		membershipEmail("gus"):  nil,
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

func TestListOrgsPagesEveryOrganizationOnceWhenKeysTie(t *testing.T) {
	t.Parallel()
	a, f := newOrgListFixture(t)
	dana := a.signUp(t, "dana")
	for _, name := range []string{"alpha", "ALPHA"} {
		if _, err := dana.clients.Org.CreateOrg(t.Context(), &orgv1.CreateOrgRequest{Name: new(name)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.pool.Exec(t.Context(), "UPDATE organizations SET created_at = '2026-01-01T00:00:00Z'"); err != nil {
		t.Fatal(err)
	}
	for _, order := range []orgv1.OrgOrder{orgv1.OrgOrder_ORG_ORDER_NAME, orgv1.OrgOrder_ORG_ORDER_CREATED_AT} {
		for _, descending := range []bool{false, true} {
			request := &orgv1.ListOrgsRequest{OrderBy: order, Descending: descending}
			whole, err := f.reader.clients.Org.ListOrgs(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			got := pageOrgIDs(t, f.reader, request)
			if want := listedOrgIDs(whole); len(want) != 5 || !slices.Equal(got, want) {
				t.Errorf("order %v descending %v paged %v, want %v", order, descending, got, want)
			}
		}
	}
}

// pageOrgIDs lists every organization in the request's order, one per page.
func pageOrgIDs(t *testing.T, user membershipUser, request *orgv1.ListOrgsRequest) []string {
	t.Helper()
	request = &orgv1.ListOrgsRequest{OrderBy: request.GetOrderBy(), Descending: request.GetDescending(), PageSize: 1}
	var ids []string
	for {
		response, err := user.clients.Org.ListOrgs(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, listedOrgIDs(response)...)
		request.PageToken = response.GetNextPageToken()
		if response.GetNextPageToken() == "" {
			return ids
		}
	}
}

func TestListOrgsPagesPastTheLongestName(t *testing.T) {
	t.Parallel()
	a, f := newOrgListFixture(t)
	longest := strings.Repeat("😀", 100)
	erin := a.signUp(t, "erin")
	if _, err := erin.clients.Org.CreateOrg(t.Context(), &orgv1.CreateOrgRequest{Name: new(longest)}); err != nil {
		t.Fatal(err)
	}
	request := &orgv1.ListOrgsRequest{OrderBy: orgv1.OrgOrder_ORG_ORDER_NAME, Descending: true, PageSize: 1}
	first, err := f.reader.clients.Org.ListOrgs(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got := first.GetOrgs()[0].GetOrganization().GetName(); got != longest {
		t.Fatalf("first name = %q, want the longest name", got)
	}
	if len(first.GetNextPageToken()) <= 512 {
		t.Fatalf("token has %d characters, want one past the old 512 limit", len(first.GetNextPageToken()))
	}
	request.PageToken = first.GetNextPageToken()
	second, err := f.reader.clients.Org.ListOrgs(t.Context(), request)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if got := listedOrgIDs(second); len(got) != 1 || got[0] != f.gamma {
		t.Fatalf("second page = %v, want gamma", got)
	}
}

func TestListOrgsRejectsATokenFromAnotherOrderOrDirection(t *testing.T) {
	t.Parallel()
	_, f := newOrgListFixture(t)
	first, err := f.reader.clients.Org.ListOrgs(t.Context(), &orgv1.ListOrgsRequest{
		PageSize: 1, OrderBy: orgv1.OrgOrder_ORG_ORDER_NAME,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, request := range map[string]*orgv1.ListOrgsRequest{
		"another order":     {OrderBy: orgv1.OrgOrder_ORG_ORDER_CREATED_AT},
		"another direction": {OrderBy: orgv1.OrgOrder_ORG_ORDER_NAME, Descending: true},
	} {
		request.PageSize, request.PageToken = 1, first.GetNextPageToken()
		_, err = f.reader.clients.Org.ListOrgs(t.Context(), request)
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: err = %v, want InvalidArgument", name, err)
		}
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
