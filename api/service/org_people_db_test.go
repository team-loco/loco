package service

import (
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

const (
	olivia     = "olivia@acme.test"
	bea        = "Bea@acme.test"
	cal        = "Cal@acme.test"
	dee        = "Dee@acme.test"
	zed        = "zed@acme.test"
	percentage = "100%@acme.test"
	shopName   = "shop"
)

type peopleFixture struct {
	tenant
	server    *OrgServer
	workspace uuid.UUID
	ops       uuid.UUID
	ids       map[string]uuid.UUID
}

// newPeopleFixture is an organization whose owner is Olivia, with members Bea (admin), Cal and Dee
// (Dee holds read on the shop workspace, Cal on ops) and open invitations for zed@ and 100%@, the
// newest last.
func newPeopleFixture(t *testing.T) peopleFixture {
	t.Helper()
	pool := dbtest.NewPool(t)
	queries := genDb.New(pool)
	tn := seedTenant(t, pool, queries, olivia, membershipOrgName)
	if _, err := pool.Exec(t.Context(), "UPDATE users SET name = 'Olivia' WHERE id = $1", tn.ownerID); err != nil {
		t.Fatal(err)
	}
	pf := peopleFixture{tenant: tn, server: NewOrgServer(pool, queries, InvitationConfig{}, limits.Defaults())}
	pf.workspace = tn.workspace(t, shopName)
	pf.ops = tn.workspace(t, "ops")
	pf.ids = map[string]uuid.UUID{}
	for _, m := range []struct {
		email     string
		scope     genDb.Scope
		workspace uuid.UUID
	}{{bea, genDb.ScopeAdmin, uuid.Nil}, {cal, genDb.ScopeMember, pf.ops}, {dee, genDb.ScopeMember, pf.workspace}} {
		var id uuid.UUID
		if err := pool.QueryRow(t.Context(),
			"INSERT INTO users (email, name) VALUES ($1, split_part($1, '@', 1)) RETURNING id", m.email,
		).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), "INSERT INTO user_scopes (user_id, scope, entity_type, entity_id) "+
			"VALUES ($1, $2, 'organization', $3)", id, m.scope, tn.orgID); err != nil {
			t.Fatal(err)
		}
		pf.ids[m.email] = id
		if m.workspace != uuid.Nil {
			if _, err := pool.Exec(t.Context(), "INSERT INTO user_scopes (user_id, scope, entity_type, entity_id) "+
				"VALUES ($1, 'read', 'workspace', $2)", id, m.workspace); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i, email := range []string{zed, percentage} {
		if _, err := pool.Exec(t.Context(),
			"INSERT INTO invitations (org_id, email, role, invited_by, created_at, expires_at) "+
				"VALUES ($1, $2, 'member', $3, $4::timestamptz, $4::timestamptz + interval '7 days')",
			tn.orgID, email, tn.ownerID, time.Now().Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	return pf
}

// list walks every page of size pageSize and names each person by email.
func (f peopleFixture) list(t *testing.T, req *orgv1.ListOrgPeopleRequest) []string {
	t.Helper()
	req.OrgId = f.orgID.String()
	var got []string
	for {
		page, err := f.server.ListOrgPeople(f.owner(t), req)
		if err != nil {
			t.Fatal(err)
		}
		for _, person := range page.GetPeople() {
			if member := person.GetMember(); member != nil {
				got = append(got, member.GetUser().GetEmail())
			} else {
				got = append(got, person.GetInvitation().GetEmail())
			}
		}
		if page.GetNextPageToken() == "" {
			return got
		}
		req.PageToken = page.GetNextPageToken()
	}
}

func TestListOrgPeopleOrdersAndFilters(t *testing.T) {
	t.Parallel()
	f := newPeopleFixture(t)
	for _, tc := range []struct {
		name      string
		req       *orgv1.ListOrgPeopleRequest
		workspace bool
		want      []string
	}{
		{"invitations newest first, then the caller, then members by name", &orgv1.ListOrgPeopleRequest{PageSize: 2}, false,
			[]string{percentage, zed, olivia, bea, cal, dee}},
		{"members", &orgv1.ListOrgPeopleRequest{PageSize: 1, View: orgv1.OrgPeopleView_ORG_PEOPLE_VIEW_MEMBERS}, false,
			[]string{olivia, bea, cal, dee}},
		{"invitations", &orgv1.ListOrgPeopleRequest{View: orgv1.OrgPeopleView_ORG_PEOPLE_VIEW_INVITATIONS}, false,
			[]string{percentage, zed}},
		{"query matches names and emails ignoring case", &orgv1.ListOrgPeopleRequest{Query: " DEE "}, false,
			[]string{dee}},
		{"query treats % as text", &orgv1.ListOrgPeopleRequest{Query: "%"}, false, []string{percentage}},
		{"query treats _ as text", &orgv1.ListOrgPeopleRequest{Query: "_"}, false, nil},
		{"workspace keeps admins and its members, no invitations", &orgv1.ListOrgPeopleRequest{PageSize: 1}, true,
			[]string{olivia, bea, dee}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.workspace {
				tc.req.WorkspaceId = f.workspace.String()
			}
			if got := f.list(t, tc.req); !slices.Equal(got, tc.want) {
				t.Fatalf("people = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestListOrgPeopleCountsAndAccess(t *testing.T) {
	t.Parallel()
	f := newPeopleFixture(t)
	first, err := f.server.ListOrgPeople(f.owner(t), &orgv1.ListOrgPeopleRequest{
		OrgId: f.orgID.String(), Query: "ACME.TEST", View: orgv1.OrgPeopleView_ORG_PEOPLE_VIEW_MEMBERS,
		PageSize: 1, IncludeTotalCount: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.GetTotalCount() != 4 || first.GetMemberCount() != 4 || first.GetInvitationCount() != 2 {
		t.Fatalf("counts = %d %d %d, want 4 4 2",
			first.GetTotalCount(), first.GetMemberCount(), first.GetInvitationCount())
	}
	// Olivia and Bea are admins, Dee holds read on shop and Cal on ops.
	if got := first.GetWorkspaceMemberCounts(); len(got) != 2 || got[f.workspace.String()] != 3 ||
		got[f.ops.String()] != 3 {
		t.Fatalf("workspace member counts = %v, want shop: 3, ops: 3", got)
	}
	dee, err := f.server.ListOrgPeople(f.owner(t), &orgv1.ListOrgPeopleRequest{OrgId: f.orgID.String(), Query: "Dee"})
	if err != nil {
		t.Fatal(err)
	}
	access := dee.GetPeople()[0].GetWorkspaces()
	if len(access) != 1 || access[0].GetWorkspaceId() != f.workspace.String() ||
		access[0].GetWorkspaceName() != shopName || !slices.Equal(access[0].GetScopes(), []string{"read"}) {
		t.Fatalf("Dee's workspace access = %v", access)
	}
	_, err = f.server.ListOrgPeople(f.owner(t), &orgv1.ListOrgPeopleRequest{
		OrgId: f.orgID.String(), WorkspaceId: uuid.NewString(),
	})
	if codeOf(err) != connect.CodeNotFound && codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("unknown workspace err = %v", err)
	}
	_, err = f.server.ListOrgPeople(f.owner(t), &orgv1.ListOrgPeopleRequest{
		OrgId: f.orgID.String(), PageToken: "bad token",
	})
	if codeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("bad token err = %v", err)
	}
}

// TestListOrgPeopleShowsOnlyReadableWorkspaces checks that a plain member sees workspace access and
// counts only for the workspaces they can read, and cannot filter by a workspace they cannot read.
func TestListOrgPeopleShowsOnlyReadableWorkspaces(t *testing.T) {
	t.Parallel()
	f := newPeopleFixture(t)
	deeCtx := scoped(t, f.queries, f.ids[dee])
	page, err := f.server.ListOrgPeople(deeCtx, &orgv1.ListOrgPeopleRequest{
		OrgId: f.orgID.String(), View: orgv1.OrgPeopleView_ORG_PEOPLE_VIEW_MEMBERS, IncludeTotalCount: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := page.GetWorkspaceMemberCounts(); len(got) != 1 || got[f.workspace.String()] != 3 {
		t.Fatalf("workspace member counts Dee sees = %v, want shop: 3", got)
	}
	access := map[string][]string{}
	for _, person := range page.GetPeople() {
		for _, w := range person.GetWorkspaces() {
			email := person.GetMember().GetUser().GetEmail()
			access[email] = append(access[email], w.GetWorkspaceName())
		}
	}
	// Olivia created both workspaces and Cal reads ops; Dee reads only shop.
	if len(access) != 2 || !slices.Equal(access[dee], []string{shopName}) ||
		!slices.Equal(access[olivia], []string{shopName}) {
		t.Fatalf("workspace access Dee sees = %v, want shop access only", access)
	}
	_, err = f.server.ListOrgPeople(deeCtx, &orgv1.ListOrgPeopleRequest{
		OrgId: f.orgID.String(), WorkspaceId: f.ops.String(),
	})
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("filter by an unreadable workspace err = %v, want PermissionDenied", err)
	}
}

// TestListOrgPeoplePagesThroughEqualNames checks that members whose lower-cased names are equal each
// appear exactly once when every page holds one person.
func TestListOrgPeoplePagesThroughEqualNames(t *testing.T) {
	t.Parallel()
	f := newPeopleFixture(t)
	twins := []string{"cal@other.test", "CAL@third.test"}
	for _, email := range twins {
		if _, err := f.pool.Exec(t.Context(),
			"WITH u AS (INSERT INTO users (email, name) VALUES ($1, 'CAL') RETURNING id) "+
				"INSERT INTO user_scopes (user_id, scope, entity_type, entity_id) "+
				"SELECT id, 'member', 'organization', $2 FROM u",
			email, f.orgID); err != nil {
			t.Fatal(err)
		}
	}
	got := f.list(t, &orgv1.ListOrgPeopleRequest{PageSize: 1, View: orgv1.OrgPeopleView_ORG_PEOPLE_VIEW_MEMBERS})
	if len(got) != 6 || !slices.Equal(got[:2], []string{olivia, bea}) || got[5] != dee {
		t.Fatalf("people = %v, want Olivia, Bea, the three Cals, Dee", got)
	}
	cals := slices.Sorted(slices.Values(got[2:5]))
	if !slices.Equal(cals, []string{"CAL@third.test", cal, "cal@other.test"}) {
		t.Fatalf("Cals = %v, want each once", got[2:5])
	}
}
