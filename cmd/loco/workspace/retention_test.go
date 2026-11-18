package workspace

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	"github.com/team-loco/loco/cmd/internal/clitest"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
)

const (
	retentionOrgID = "0199e0a4-0000-7000-8000-0000000000a1"
	retentionTable = `SIGNAL   WORKSPACE  ORGANIZATION LIMIT  APPLIED
logs     30 days    90 days             30 days
traces   -          90 days             90 days
metrics  120 days   60 days             60 days
`
)

type retentionServer struct {
	workspacev1connect.UnimplementedWorkspaceServiceHandler
	orgv1connect.UnimplementedOrgServiceHandler

	t       *testing.T
	updated *workspacev1.UpdateWorkspaceRequest
}

func (s *retentionServer) GetWorkspace(
	_ context.Context,
	req *workspacev1.GetWorkspaceRequest,
) (*workspacev1.GetWorkspaceResponse, error) {
	if req.GetWorkspaceId() != testWorkspaceID {
		s.t.Errorf("workspace ID = %q", req.GetWorkspaceId())
	}
	return &workspacev1.GetWorkspaceResponse{Workspace: &workspacev1.Workspace{
		Id:        testWorkspaceID,
		OrgId:     retentionOrgID,
		Retention: &workspacev1.RetentionDays{LogDays: new(int32(30)), MetricDays: new(int32(120))},
		EffectiveRetention: &workspacev1.RetentionDays{
			LogDays: new(int32(30)), TraceDays: new(int32(90)), MetricDays: new(int32(60)),
		},
	}}, nil
}

func (s *retentionServer) GetOrgLimits(
	_ context.Context,
	req *orgv1.GetOrgLimitsRequest,
) (*orgv1.GetOrgLimitsResponse, error) {
	if req.GetOrgId() != retentionOrgID {
		s.t.Errorf("org ID = %q", req.GetOrgId())
	}
	return &orgv1.GetOrgLimitsResponse{Limits: &orgv1.OrgLimits{Limits: &configv1.CountLimits{
		LogRetentionDays: 90, TraceRetentionDays: 90, MetricRetentionDays: 60,
	}}}, nil
}

func (s *retentionServer) UpdateWorkspace(
	_ context.Context,
	req *workspacev1.UpdateWorkspaceRequest,
) (*workspacev1.UpdateWorkspaceResponse, error) {
	s.updated = req
	return &workspacev1.UpdateWorkspaceResponse{WorkspaceId: req.GetWorkspaceId()}, nil
}

// runRetentionCmd runs the workspace command with args against fake, signed in, and returns
// what it printed.
func runRetentionCmd(t *testing.T, fake *retentionServer, args ...string) (string, error) {
	t.Helper()
	clitest.Session(t, func(server *connect.Server) {
		workspacev1connect.RegisterWorkspaceServiceHandler(server, fake)
		orgv1connect.RegisterOrgServiceHandler(server, fake)
	})
	var stdout bytes.Buffer
	cmd := BuildWorkspaceCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetOut(&stdout)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), err
}

func TestWorkspaceRetentionGetPrintsEachSignal(t *testing.T) {
	out, err := runRetentionCmd(t, &retentionServer{t: t}, "retention", "get", testWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if out != retentionTable {
		t.Fatalf("output =\n%s\nwant\n%s", out, retentionTable)
	}
}

func TestWorkspaceRetentionSetSendsOnlyTheGivenSignals(t *testing.T) {
	fake := &retentionServer{t: t}
	out, err := runRetentionCmd(t, fake, "retention", "set", testWorkspaceID, "--logs", "30", "--metrics", "120")
	if err != nil {
		t.Fatal(err)
	}
	want := &workspacev1.UpdateWorkspaceRequest{
		WorkspaceId: testWorkspaceID, LogRetentionDays: new(int32(30)), MetricRetentionDays: new(int32(120)),
	}
	if !proto.Equal(fake.updated, want) {
		t.Fatalf("update = %v, want %v", fake.updated, want)
	}
	if out != retentionTable {
		t.Fatalf("output =\n%s", out)
	}
}

func TestWorkspaceRetentionUnsetClearsTheNamedSignals(t *testing.T) {
	fake := &retentionServer{t: t}
	if _, err := runRetentionCmd(t, fake, "retention", "unset", testWorkspaceID, "traces", "logs"); err != nil {
		t.Fatal(err)
	}
	want := &workspacev1.UpdateWorkspaceRequest{
		WorkspaceId: testWorkspaceID, LogRetentionDays: new(int32(0)), TraceRetentionDays: new(int32(0)),
	}
	if !proto.Equal(fake.updated, want) {
		t.Fatalf("update = %v, want %v", fake.updated, want)
	}
}

func TestWorkspaceRetentionRefusesUnknownOrMissingSignals(t *testing.T) {
	fake := &retentionServer{t: t}
	if _, err := runRetentionCmd(t, fake, "retention", "unset", testWorkspaceID, "events"); !errors.Is(
		err, errUnknownSignal,
	) {
		t.Fatalf("unset of an unknown signal: %v", err)
	}
	if _, err := runRetentionCmd(t, fake, "retention", "set", testWorkspaceID); !errors.Is(err, errNoSignals) {
		t.Fatalf("set without a signal: %v", err)
	}
	if fake.updated != nil {
		t.Fatalf("refused command sent %v", fake.updated)
	}
}
