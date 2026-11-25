package org

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/cmd/internal/clitest"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
)

const (
	testReason      = "large customer"
	limitsCmd       = "limits"
	limitsSet       = "set"
	limitsUnset     = "unset"
	reasonFlag      = "--reason"
	workspacesField = "workspaces_per_org"
)

type fakeOrgs struct {
	orgv1connect.UnimplementedOrgServiceHandler
	sets  []*orgv1.SetOrgLimitsRequest
	lists []*orgv1.ListOrgsRequest
}

func (*fakeOrgs) GetOrgLimits(
	_ context.Context,
	req *orgv1.GetOrgLimitsRequest,
) (*orgv1.GetOrgLimitsResponse, error) {
	return &orgv1.GetOrgLimitsResponse{Limits: &orgv1.OrgLimits{
		Limits:     &configv1.CountLimits{WorkspacesPerOrg: 500},
		Overridden: []string{workspacesField},
		Reason:     req.GetOrgId(),
	}}, nil
}

func (f *fakeOrgs) SetOrgLimits(
	_ context.Context,
	req *orgv1.SetOrgLimitsRequest,
) (*orgv1.SetOrgLimitsResponse, error) {
	f.sets = append(f.sets, req)
	return &orgv1.SetOrgLimitsResponse{Limits: &orgv1.OrgLimits{Limits: req.GetLimits()}}, nil
}

func runOrgCmd(t *testing.T, fake *fakeOrgs, args ...string) (string, error) {
	t.Helper()
	clitest.Session(t, func(server *connect.Server) {
		orgv1connect.RegisterOrgServiceHandler(server, fake)
	})
	cmd := BuildOrgCmd()
	output := new(bytes.Buffer)
	cmd.SetOut(output)
	cmd.SetErr(output)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return output.String(), err
}

func TestLimitsGetPrintsTheEffectiveLimits(t *testing.T) {
	orgID := uuid.NewString()
	output, err := runOrgCmd(t, &fakeOrgs{}, limitsCmd, "get", orgID)
	if err != nil {
		t.Fatalf("get: %v\n%s", err, output)
	}
	var printed orgv1.GetOrgLimitsResponse
	if err = protojson.Unmarshal([]byte(output), &printed); err != nil {
		t.Fatalf("output %q is not a GetOrgLimitsResponse: %v", output, err)
	}
	if printed.GetLimits().GetLimits().GetWorkspacesPerOrg() != 500 || printed.GetLimits().GetReason() != orgID {
		t.Fatalf("output = %s, want the limits of %s", output, orgID)
	}
}

func TestLimitsSetSendsOnlyTheNamedLimits(t *testing.T) {
	fake := &fakeOrgs{}
	orgID := uuid.NewString()
	output, err := runOrgCmd(t, fake, limitsCmd, limitsSet, orgID,
		"--workspaces-per-org", "500", "--deployment-history", "20", reasonFlag, testReason)
	if err != nil {
		t.Fatalf("set: %v\n%s", err, output)
	}
	if len(fake.sets) != 1 {
		t.Fatalf("%d SetOrgLimits calls, want 1", len(fake.sets))
	}
	req := fake.sets[0]
	paths := req.GetUpdateMask().GetPaths()
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"deployment_history", workspacesField}) ||
		req.GetLimits().GetWorkspacesPerOrg() != 500 || req.GetLimits().GetDeploymentHistory() != 20 ||
		req.GetOrgId() != orgID || req.GetReason() != testReason {
		t.Fatalf("request = %v", req)
	}
}

func TestLimitsUnsetClearsTheNamedLimits(t *testing.T) {
	fake := &fakeOrgs{}
	output, err := runOrgCmd(
		t,
		fake,
		limitsCmd,
		limitsUnset,
		uuid.NewString(),
		"workspaces-per-org",
		reasonFlag,
		testReason,
	)
	if err != nil {
		t.Fatalf("unset: %v\n%s", err, output)
	}
	req := fake.sets[0]
	if !slices.Equal(req.GetUpdateMask().GetPaths(), []string{workspacesField}) ||
		req.GetLimits().GetWorkspacesPerOrg() != 0 {
		t.Fatalf("request = %v, want workspaces_per_org cleared", req)
	}
}

func TestLimitsChangesNeedAReasonAndALimit(t *testing.T) {
	orgID := uuid.NewString()
	for name, args := range map[string][]string{
		"set without a reason":   {limitsCmd, limitsSet, orgID, "--workspaces-per-org", "500"},
		"unset without a reason": {limitsCmd, limitsUnset, orgID, "workspaces-per-org"},
		"set without a limit":    {limitsCmd, limitsSet, orgID, reasonFlag, testReason},
		"unset an unknown limit": {limitsCmd, limitsUnset, orgID, "pods", reasonFlag, testReason},
		"set a file limit":       {limitsCmd, limitsSet, orgID, "--services-per-file", "500", reasonFlag, testReason},
		"unset a user limit":     {limitsCmd, limitsUnset, orgID, "orgs-per-user", reasonFlag, testReason},
	} {
		t.Run(name, func(t *testing.T) {
			fake := &fakeOrgs{}
			if _, err := runOrgCmd(t, fake, args...); err == nil || len(fake.sets) != 0 {
				t.Fatalf("err = %v after %d calls, want a refusal before any call", err, len(fake.sets))
			}
		})
	}
}

func (f *fakeOrgs) ListOrgs(_ context.Context, req *orgv1.ListOrgsRequest) (*orgv1.ListOrgsResponse, error) {
	f.lists = append(f.lists, req)
	if req.GetPageToken() == "" {
		return &orgv1.ListOrgsResponse{NextPageToken: "next", Orgs: []*orgv1.OrgSummary{{
			Organization: &orgv1.Organization{
				Id: "org-1", Name: "Acme", CreatedAt: timestamppb.New(time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)),
				WorkspaceCount: new(int64(2)), MemberCount: new(int64(3)),
			},
			Owners:        []*orgv1.User{{Email: "dana@acme.test"}, {Email: "erin@acme.test"}},
			ResourceCount: 5,
		}}}, nil
	}
	return &orgv1.ListOrgsResponse{Orgs: []*orgv1.OrgSummary{{
		Organization: &orgv1.Organization{
			Id: "org-2", Name: "Beta", SuspendedAt: timestamppb.Now(), SuspensionReason: "unpaid",
			CreatedAt: timestamppb.Now(), WorkspaceCount: new(int64(0)), MemberCount: new(int64(1)),
		},
		Owners: []*orgv1.User{{Email: "fay@beta.test"}},
	}}}, nil
}

func TestListPrintsEveryPageAsATableAndSendsTheFilters(t *testing.T) {
	fake := &fakeOrgs{}
	output, err := runOrgCmd(t, fake, "list", "--query", "acme", "--state", "suspended")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, output)
	}
	if len(fake.lists) != 2 || fake.lists[0].GetQuery() != "acme" ||
		fake.lists[0].GetState() != orgv1.OrgState_ORG_STATE_SUSPENDED || fake.lists[1].GetPageToken() != "next" {
		t.Fatalf("requests = %v, want the query and state on both pages", fake.lists)
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "ID") {
		t.Fatalf("output = %q, want a header and two rows", output)
	}
	for i, want := range [][]string{
		{"org-1", "Acme", "active", "dana@acme.test,erin@acme.test", "3", "2", "5"},
		{"org-2", "Beta", "suspended:", "unpaid", "fay@beta.test", "1", "0", "0"},
	} {
		if got := strings.Fields(lines[i+1]); !slices.Equal(got[:len(want)], want) {
			t.Errorf("row %d = %v, want it to start with %v", i, got, want)
		}
	}
}

func TestListRejectsAnUnknownState(t *testing.T) {
	fake := &fakeOrgs{}
	if _, err := runOrgCmd(t, fake, "list", "--state", "gone"); err == nil || len(fake.lists) != 0 {
		t.Fatalf("err = %v after %d calls, want a refusal before any call", err, len(fake.lists))
	}
}
