package org

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/team-loco/loco/cmd/internal/keychain"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	"github.com/team-loco/loco/internal/bearer"
)

const (
	testReason      = "large customer"
	limitsCmd       = "limits"
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
	session := bearer.New("")
	server := connect.NewServer()
	orgv1connect.RegisterOrgServiceHandler(server, fake)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	endpoint := httptest.NewServer(mux)
	t.Cleanup(endpoint.Close)
	t.Setenv("LOCO_HOST", endpoint.URL)
	t.Setenv("HOME", t.TempDir())
	t.Setenv(keychain.StoreEnvVar, "file")
	store, err := keychain.ForCurrentUser()
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Set(keychain.UserToken{
		Host: endpoint.URL, Token: session, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	cmd := BuildOrgCmd()
	output := new(bytes.Buffer)
	cmd.SetOut(output)
	cmd.SetErr(output)
	cmd.SetArgs(args)
	err = cmd.Execute()
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
	output, err := runOrgCmd(t, fake, limitsCmd, "set", orgID,
		"--workspaces-per-org", "500", "--deployment-history", "20", "--reason", testReason)
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
		"unset",
		uuid.NewString(),
		"workspaces-per-org",
		"--reason",
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
		"set without a reason":   {limitsCmd, "set", orgID, "--workspaces-per-org", "500"},
		"unset without a reason": {limitsCmd, "unset", orgID, "workspaces-per-org"},
		"set without a limit":    {limitsCmd, "set", orgID, "--reason", testReason},
		"unset an unknown limit": {limitsCmd, "unset", orgID, "pods", "--reason", testReason},
	} {
		t.Run(name, func(t *testing.T) {
			fake := &fakeOrgs{}
			if _, err := runOrgCmd(t, fake, args...); err == nil || len(fake.sets) != 0 {
				t.Fatalf("err = %v after %d calls, want a refusal before any call", err, len(fake.sets))
			}
		})
	}
}
