package org

import (
	"bytes"
	"context"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"

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
	sets []*orgv1.SetOrgLimitsRequest
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
