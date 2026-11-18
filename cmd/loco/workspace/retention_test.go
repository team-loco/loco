package workspace

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	"github.com/team-loco/loco/cmd/internal/clitest"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
)

const retentionTable = `SIGNAL   WORKSPACE  ORGANIZATION LIMIT  APPLIED
logs     30 days    90 days             30 days
traces   -          90 days             90 days
metrics  120 days   60 days             60 days
`

type retentionServer struct {
	workspacev1connect.UnimplementedWorkspaceServiceHandler
	t   *testing.T
	set *workspacev1.SetWorkspaceRetentionRequest
}

func testRetention() *workspacev1.WorkspaceRetention {
	limits := &workspacev1.RetentionDays{LogDays: new(int32(90)), TraceDays: new(int32(90)), MetricDays: new(int32(60))}
	return &workspacev1.WorkspaceRetention{
		Settings: &workspacev1.RetentionDays{LogDays: new(int32(30)), MetricDays: new(int32(120))},
		Limits:   limits,
		Effective: &workspacev1.RetentionDays{
			LogDays:    new(int32(30)),
			TraceDays:  new(int32(90)),
			MetricDays: new(int32(60)),
		},
	}
}

func (s *retentionServer) GetWorkspaceRetention(
	_ context.Context,
	req *workspacev1.GetWorkspaceRetentionRequest,
) (*workspacev1.GetWorkspaceRetentionResponse, error) {
	if req.GetWorkspaceId() != testWorkspaceID {
		s.t.Errorf("workspace ID = %q", req.GetWorkspaceId())
	}
	return &workspacev1.GetWorkspaceRetentionResponse{Retention: testRetention()}, nil
}

func (s *retentionServer) SetWorkspaceRetention(
	_ context.Context,
	req *workspacev1.SetWorkspaceRetentionRequest,
) (*workspacev1.SetWorkspaceRetentionResponse, error) {
	s.set = req
	return &workspacev1.SetWorkspaceRetentionResponse{Retention: testRetention()}, nil
}

// runWorkspaceCmd runs the workspace command with args against fake, signed in, and returns
// what it printed.
func runWorkspaceCmd(t *testing.T, fake workspacev1connect.WorkspaceServiceHandler, args ...string) (string, error) {
	t.Helper()
	clitest.Session(t, func(server *connect.Server) {
		workspacev1connect.RegisterWorkspaceServiceHandler(server, fake)
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
	out, err := runWorkspaceCmd(t, &retentionServer{t: t}, "retention", "get", testWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if out != retentionTable {
		t.Fatalf("output =\n%s\nwant\n%s", out, retentionTable)
	}
}

func TestWorkspaceRetentionSetSendsOnlyTheGivenSignals(t *testing.T) {
	fake := &retentionServer{t: t}
	out, err := runWorkspaceCmd(t, fake, "retention", "set", testWorkspaceID, "--logs", "30", "--metrics", "120")
	if err != nil {
		t.Fatal(err)
	}
	want := &workspacev1.RetentionDays{LogDays: new(int32(30)), MetricDays: new(int32(120))}
	if fake.set.GetWorkspaceId() != testWorkspaceID || !proto.Equal(fake.set.GetSettings(), want) ||
		!slices.Equal(fake.set.GetUpdateMask().GetPaths(), []string{"log_days", "metric_days"}) {
		t.Fatalf("set request = %v", fake.set)
	}
	if out != retentionTable {
		t.Fatalf("output =\n%s", out)
	}
}

func TestWorkspaceRetentionUnsetClearsTheNamedSignals(t *testing.T) {
	fake := &retentionServer{t: t}
	if _, err := runWorkspaceCmd(t, fake, "retention", "unset", testWorkspaceID, "traces", "logs"); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(fake.set.GetSettings(), &workspacev1.RetentionDays{}) ||
		!slices.Equal(fake.set.GetUpdateMask().GetPaths(), []string{"trace_days", "log_days"}) {
		t.Fatalf("unset request = %v", fake.set)
	}
}

func TestWorkspaceRetentionRefusesUnknownOrMissingSignals(t *testing.T) {
	fake := &retentionServer{t: t}
	if _, err := runWorkspaceCmd(t, fake, "retention", "unset", testWorkspaceID, "events"); !errors.Is(
		err, errUnknownSignal,
	) {
		t.Fatalf("unset of an unknown signal: %v", err)
	}
	if _, err := runWorkspaceCmd(t, fake, "retention", "set", testWorkspaceID); !errors.Is(err, errNoSignals) {
		t.Fatalf("set without a signal: %v", err)
	}
	if fake.set != nil {
		t.Fatalf("refused command sent %v", fake.set)
	}
}
