package cluster

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/keychain"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/gen/go/loco/cluster/v1/clusterv1connect"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/internal/rpc"
	"github.com/team-loco/loco/internal/rpcerr"
)

const (
	testClusterID = "00000000-0000-7000-8000-000000000001"
	testJoinToken = "loco_j_test"
	testName      = "east"
	testRegion    = "us-east-1"
	testTier      = environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION
)

var (
	testTierFlag = strings.ToLower(strings.TrimPrefix(testTier.String(), tierEnumPrefix))
	createArgs   = []string{
		clusterCreate, "--" + flagName, testName, "--" + flagRegion, testRegion, "--" + flagTier, testTierFlag,
	}
)

var errTestDenied = errors.New("permission denied")

type fakeClusterAdmin struct {
	clusterv1connect.UnimplementedClusterAdminServiceHandler
	t       *testing.T
	calls   []string
	force   bool
	created *clusterv1.CreateClusterRequest
	err     error
}

func (f *fakeClusterAdmin) record(ctx context.Context, method, id string) error {
	f.t.Helper()
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok || info.RequestHeader().Get("Authorization") != "Bearer ordinary-credential" {
		f.t.Error("CLI did not authenticate the request")
	}
	if id != "" && id != testClusterID {
		f.t.Errorf("cluster ID: %s", id)
	}
	f.calls = append(f.calls, method)
	return f.err
}

func (f *fakeClusterAdmin) CreateCluster(
	ctx context.Context,
	req *clusterv1.CreateClusterRequest,
) (*clusterv1.CreateClusterResponse, error) {
	if err := f.record(ctx, clusterCreate, ""); err != nil {
		return nil, err
	}
	f.created = req
	return &clusterv1.CreateClusterResponse{
		ClusterId: testClusterID,
		JoinToken: &clusterv1.JoinToken{Token: testJoinToken},
	}, nil
}

func (f *fakeClusterAdmin) IssueJoinToken(
	ctx context.Context,
	req *clusterv1.IssueJoinTokenRequest,
) (*clusterv1.IssueJoinTokenResponse, error) {
	if err := f.record(ctx, clusterJoinToken, req.GetClusterId()); err != nil {
		return nil, err
	}
	return &clusterv1.IssueJoinTokenResponse{
		ClusterId: req.GetClusterId(),
		JoinToken: &clusterv1.JoinToken{Token: testJoinToken},
	}, nil
}

func (f *fakeClusterAdmin) ListClusters(
	ctx context.Context,
	_ *clusterv1.ListClustersRequest,
) (*clusterv1.ListClustersResponse, error) {
	if err := f.record(ctx, clusterList, ""); err != nil {
		return nil, err
	}
	return &clusterv1.ListClustersResponse{Clusters: []*clusterv1.Cluster{{Id: testClusterID, Name: testName}}}, nil
}

func (f *fakeClusterAdmin) PauseCluster(
	ctx context.Context,
	req *clusterv1.PauseClusterRequest,
) (*clusterv1.PauseClusterResponse, error) {
	if err := f.record(ctx, clusterPause, req.GetClusterId()); err != nil {
		return nil, err
	}
	return &clusterv1.PauseClusterResponse{ClusterId: req.GetClusterId()}, nil
}

func (f *fakeClusterAdmin) ResumeCluster(
	ctx context.Context,
	req *clusterv1.ResumeClusterRequest,
) (*clusterv1.ResumeClusterResponse, error) {
	if err := f.record(ctx, clusterResume, req.GetClusterId()); err != nil {
		return nil, err
	}
	return &clusterv1.ResumeClusterResponse{ClusterId: req.GetClusterId()}, nil
}

func (f *fakeClusterAdmin) RemoveCluster(
	ctx context.Context,
	req *clusterv1.RemoveClusterRequest,
) (*clusterv1.RemoveClusterResponse, error) {
	if err := f.record(ctx, clusterRemove, req.GetClusterId()); err != nil {
		return nil, err
	}
	f.force = req.GetForce()
	return &clusterv1.RemoveClusterResponse{ClusterId: req.GetClusterId()}, nil
}

func (f *fakeClusterAdmin) RestoreCluster(
	ctx context.Context,
	req *clusterv1.RestoreClusterRequest,
) (*clusterv1.RestoreClusterResponse, error) {
	if err := f.record(ctx, clusterRestore, req.GetClusterId()); err != nil {
		return nil, err
	}
	return &clusterv1.RestoreClusterResponse{ClusterId: req.GetClusterId()}, nil
}

func clusterTestCommand(t *testing.T, fake *fakeClusterAdmin) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	server := connect.NewServer()
	clusterv1connect.RegisterClusterAdminServiceHandler(server, fake)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	endpoint := httptest.NewServer(mux)
	t.Cleanup(endpoint.Close)
	t.Setenv("LOCO_HOST", endpoint.URL)
	deps := dependencies{
		token: func(*cobra.Command) (*keychain.UserToken, error) {
			return &keychain.UserToken{Host: endpoint.URL, Token: "ordinary-credential"}, nil
		},
		client: func(host, token string) clusterv1connect.ClusterAdminServiceClient {
			return rpc.NewHTTPClients(endpoint.Client(), host, token).ClusterAdmin
		},
	}
	cmd := newClusterCmd(deps)
	output := new(bytes.Buffer)
	cmd.SetOut(output)
	cmd.SetErr(output)
	return cmd, output
}

func TestClusterCommandsUseAuthenticatedHTTP(t *testing.T) {
	for _, method := range []string{clusterList, clusterPause, clusterResume, clusterRemove, clusterRestore} {
		t.Run(method, func(t *testing.T) {
			fake := &fakeClusterAdmin{t: t}
			cmd, output := clusterTestCommand(t, fake)
			args := []string{method}
			if method != clusterList {
				args = append(args, testClusterID)
			}
			if method == clusterRemove {
				args = append(args, "--force")
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if len(fake.calls) != 1 || fake.calls[0] != method || !strings.Contains(output.String(), testClusterID) {
				t.Fatalf("calls/output: %v, %s", fake.calls, output)
			}
			if method == clusterRemove && !fake.force {
				t.Fatal("force was not forwarded")
			}
		})
	}
}

func TestClusterCreateAndJoinTokenPrintTheToken(t *testing.T) {
	for _, args := range [][]string{
		createArgs,
		{clusterJoinToken, testClusterID},
	} {
		fake := &fakeClusterAdmin{t: t}
		cmd, output := clusterTestCommand(t, fake)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if len(fake.calls) != 1 || fake.calls[0] != args[0] {
			t.Fatalf("calls: %v", fake.calls)
		}
		if !strings.Contains(output.String(), testJoinToken) || !strings.Contains(output.String(), testClusterID) {
			t.Fatalf("output lacks the cluster ID or join token: %s", output)
		}
		if args[0] == clusterCreate &&
			(fake.created.GetName() != testName || fake.created.GetRegion() != testRegion ||
				fake.created.GetTier() != testTier) {
			t.Fatalf("created %v", fake.created)
		}
	}
}

func TestClusterPreservesDenialsAndPrintsBlockingWorkloads(t *testing.T) {
	detail, err := connectproto.NewErrorDetail(
		&clusterv1.ClusterWorkloads{Workloads: []*clusterv1.ClusterWorkload{{Id: testClusterID, Kind: "placement"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []*connect.Error{
		rpcerr.New(connect.CodePermissionDenied, errTestDenied),
		rpcerr.New(connect.CodeFailedPrecondition, errTestDenied).WithDetail(detail),
	} {
		fake := &fakeClusterAdmin{t: t, err: failure}
		cmd, output := clusterTestCommand(t, fake)
		cmd.SetArgs([]string{clusterRemove, testClusterID})
		if err := cmd.Execute(); connect.CodeOf(err) != failure.Code() {
			t.Fatalf("error: %v", err)
		}
		if failure.Code() == connect.CodeFailedPrecondition &&
			!strings.Contains(output.String(), "placement "+testClusterID) {
			t.Fatalf("missing blocker: %s", output)
		}
	}
}

func TestClusterRejectsBadArgumentsBeforeRPC(t *testing.T) {
	for _, args := range [][]string{
		{clusterPause},
		{clusterPause, "invalid"},
		{clusterList, testClusterID},
		{clusterResume, testClusterID, "--force"},
		createArgs[:len(createArgs)-2],
		append([]string{clusterCreate, testClusterID}, createArgs[1:]...),
		append(createArgs[:len(createArgs)-1:len(createArgs)-1], "qa"),
		{clusterJoinToken},
		{clusterJoinToken, "invalid"},
	} {
		fake := &fakeClusterAdmin{t: t}
		cmd, _ := clusterTestCommand(t, fake)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted %v", args)
		}
		if len(fake.calls) != 0 {
			t.Fatalf("made RPC for invalid args: %v", args)
		}
	}
}
