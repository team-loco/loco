package service

import (
	"slices"
	"testing"

	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestListOrgWorkspacesSearchesFiltersAndCounts(t *testing.T) {
	t.Parallel()
	o := newMembershipOrg(t)
	member := o.api.signUp(t, "member")
	o.addOrgMember(t, member)
	o.grantWorkspace(t, member, o.shared)
	o.sharedResource(t, "api")
	ctx := t.Context()

	list := func(user membershipUser, req *workspacev1.ListOrgWorkspacesRequest) ([]string, int32) {
		t.Helper()
		req.OrgId = o.orgID
		req.IncludeTotalCount = true
		listed, err := user.clients.Workspace.ListOrgWorkspaces(ctx, req)
		if err != nil {
			t.Fatalf("%s list org workspaces %+v: %v", user.subject, req, err)
		}
		ids := make([]string, 0, len(listed.GetWorkspaces()))
		for _, ws := range listed.GetWorkspaces() {
			ids = append(ids, ws.GetId())
		}
		return ids, listed.GetTotalCount()
	}

	all, total := list(o.owner, &workspacev1.ListOrgWorkspacesRequest{})
	if !slices.Contains(all, o.shared) || !slices.Contains(all, o.sibling) || total != int32(len(all)) {
		t.Fatalf("owner lists %v with total %d, want shared and sibling counted", all, total)
	}
	for _, tc := range []struct {
		name string
		req  *workspacev1.ListOrgWorkspacesRequest
		want []string
	}{
		{"name ignoring case", &workspacev1.ListOrgWorkspacesRequest{Query: "SHAR"}, []string{o.shared}},
		{"like wildcards are literal", &workspacev1.ListOrgWorkspacesRequest{Query: "s%d"}, nil},
		{"with resources", &workspacev1.ListOrgWorkspacesRequest{HasResources: true}, []string{o.shared}},
		{"query and resources", &workspacev1.ListOrgWorkspacesRequest{Query: "sib", HasResources: true}, nil},
	} {
		got, matched := list(o.owner, tc.req)
		if !slices.Equal(got, tc.want) || matched != int32(len(tc.want)) {
			t.Errorf("%s: listed %v with total %d, want %v", tc.name, got, matched, tc.want)
		}
	}
	paged, pagedTotal := list(o.owner, &workspacev1.ListOrgWorkspacesRequest{PageSize: 1})
	if len(paged) != 1 || pagedTotal != int32(len(all)) {
		t.Errorf("first page of one lists %v with total %d, want one row and total %d", paged, pagedTotal, len(all))
	}
	if got, matched := list(member, &workspacev1.ListOrgWorkspacesRequest{}); !slices.Equal(got, []string{o.shared}) ||
		matched != 1 {
		t.Errorf("member lists %v with total %d, want only shared", got, matched)
	}

	user, err := member.clients.Workspace.ListUserWorkspaces(ctx, &workspacev1.ListUserWorkspacesRequest{
		IncludeTotalCount: true,
	})
	if err != nil {
		t.Fatalf("list user workspaces: %v", err)
	}
	if user.GetTotalCount() != int32(len(user.GetWorkspaces())) || user.GetTotalCount() == 0 {
		t.Errorf("user workspaces total %d for %d rows", user.GetTotalCount(), len(user.GetWorkspaces()))
	}

	got, err := member.clients.Workspace.GetWorkspace(ctx, &workspacev1.GetWorkspaceRequest{WorkspaceId: o.shared})
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if ws := got.GetWorkspace(); ws.ResourceCount == nil || ws.GetResourceCount() != 1 || ws.GetMemberCount() != 2 {
		t.Errorf("get workspace counts %v resources and %v members, want 1 and 2", ws.ResourceCount, ws.MemberCount)
	}
}
