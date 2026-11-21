package cluster

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"google.golang.org/protobuf/types/known/timestamppb"

	"sigs.k8s.io/yaml"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/clitest"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/gen/go/loco/cluster/v1/clusterv1connect"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/internal/rpcerr"
)

const (
	testClusterID     = "00000000-0000-7000-8000-000000000001"
	testJoinToken     = "loco_j_test"
	testName          = "east"
	testWorkerContext = "worker"
	testRegion        = "us-ca-1"
)

var (
	createArgs = []string{
		clusterCreate, "--" + flagName, testName, "--" + flagRegion, testRegion,
	}
)

var errTestDenied = errors.New("permission denied")

type fakeClusterAdmin struct {
	clusterv1connect.UnimplementedClusterAdminServiceHandler
	t          *testing.T
	calls      []string
	force      bool
	created    *clusterv1.CreateClusterRequest
	expiresAt  *timestamppb.Timestamp
	err        error
	credential string
}

func (f *fakeClusterAdmin) record(ctx context.Context, method, id string) error {
	f.t.Helper()
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok || info.RequestHeader().Get("Authorization") != "Bearer "+f.credential {
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
		Region:    testRegion,
		JoinToken: &clusterv1.JoinToken{Token: testJoinToken, ExpiresAt: f.expiresAt},
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
		JoinToken: &clusterv1.JoinToken{Token: testJoinToken, ExpiresAt: f.expiresAt},
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
	fake.credential = clitest.Session(t, func(server *connect.Server) {
		clusterv1connect.RegisterClusterAdminServiceHandler(server, fake)
	})
	cmd := BuildClusterCmd()
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
			(fake.created.GetName() != testName || fake.created.GetRegion() != testRegion) {
			t.Fatalf("created %v", fake.created)
		}
	}
}

func TestClusterCreateDescribesTheRegionToCreate(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want *clusterv1.CreateClusterRequest
	}{
		{
			args: []string{clusterCreate, "--" + flagCountry, "us", "--" + flagSubdivision, "ca"},
			want: &clusterv1.CreateClusterRequest{Target: &clusterv1.CreateClusterRequest_NewRegion{
				NewRegion: &clusterv1.NewRegion{Country: "us", Subdivision: "ca"},
			}},
		},
		{
			args: []string{clusterCreate, "--" + flagLocal},
			want: &clusterv1.CreateClusterRequest{Target: &clusterv1.CreateClusterRequest_Region{Region: "local-1"}},
		},
	} {
		fake := &fakeClusterAdmin{t: t}
		cmd, _ := clusterTestCommand(t, fake)
		cmd.SetArgs(tc.args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		got := &clusterv1.CreateClusterRequest{Name: fake.created.GetName(), Target: fake.created.GetTarget()}
		if !proto.Equal(got, tc.want) {
			t.Fatalf("%v created %v, want %v", tc.args, fake.created, tc.want)
		}
	}
	for _, args := range [][]string{
		{clusterCreate},
		{clusterCreate, "--" + flagRegion, testRegion, "--" + flagLocal},
		{clusterCreate, "--" + flagCountry, "us"},
	} {
		fake := &fakeClusterAdmin{t: t}
		cmd, _ := clusterTestCommand(t, fake)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil || len(fake.calls) != 0 {
			t.Fatalf("%v ran with calls %v, want a flag error", args, fake.calls)
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
		{clusterJoinToken},
		{clusterJoinToken, "invalid"},
		{clusterJoinToken, testClusterID, "-o", "unsupported"},
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

func TestClusterCreatePrintsSecretWithRequestedTTL(t *testing.T) {
	fake := &fakeClusterAdmin{t: t}
	command, output := clusterTestCommand(t, fake)
	args := append(
		append([]string{}, createArgs...),
		"--ttl",
		"72h",
		"-o",
		"secret",
		"--namespace",
		testWorkerContext,
		"--secret-name",
		"join",
	)
	command.SetArgs(args)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if fake.created.GetTtl().AsDuration() != 72*time.Hour {
		t.Fatalf("ttl = %v", fake.created.GetTtl())
	}
	var secret struct {
		APIVersion string                           `json:"apiVersion"`
		Kind       string                           `json:"kind"`
		Metadata   struct{ Name, Namespace string } `json:"metadata"`
		StringData map[string]string                `json:"stringData"`
	}
	if err := yaml.Unmarshal(output.Bytes(), &secret); err != nil {
		t.Fatal(err)
	}
	if secret.APIVersion != "v1" || secret.Kind != "Secret" || secret.Metadata.Name != "join" ||
		secret.Metadata.Namespace != testWorkerContext ||
		secret.StringData["join-token"] != testJoinToken {
		t.Fatalf("invalid join Secret: %+v", secret)
	}
}

type fakeDefaultRegion struct {
	configv1connect.UnimplementedConfigServiceHandler
	clusterv1connect.UnimplementedRegionAdminServiceHandler

	configured string
	regions    []string
}

func (f *fakeDefaultRegion) GetConfig(
	context.Context,
	*configv1.GetConfigRequest,
) (*configv1.GetConfigResponse, error) {
	return &configv1.GetConfigResponse{DefaultRegion: f.configured}, nil
}

func (f *fakeDefaultRegion) ListRegions(
	context.Context,
	*clusterv1.ListRegionsRequest,
) (*clusterv1.ListRegionsResponse, error) {
	resp := &clusterv1.ListRegionsResponse{}
	for _, code := range f.regions {
		resp.Regions = append(resp.Regions, &clusterv1.Region{Code: code})
	}
	return resp, nil
}

func TestClusterCreateSuggestsTheFirstRegionAsTheDefault(t *testing.T) {
	const hint = "Set LOCO_DEFAULT_REGION=" + testRegion
	for _, tc := range []struct {
		name string
		api  *fakeDefaultRegion
		want bool
	}{
		{"first region without a default", &fakeDefaultRegion{regions: []string{testRegion}}, true},
		{"default already set", &fakeDefaultRegion{configured: testRegion, regions: []string{testRegion}}, false},
		{"second region", &fakeDefaultRegion{regions: []string{testRegion, "us-va-1"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeClusterAdmin{t: t}
			fake.credential = clitest.Session(t, func(server *connect.Server) {
				clusterv1connect.RegisterClusterAdminServiceHandler(server, fake)
				configv1connect.RegisterConfigServiceHandler(server, tc.api)
				clusterv1connect.RegisterRegionAdminServiceHandler(server, tc.api)
			})
			cmd := BuildClusterCmd()
			stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
			cmd.SetOut(stdout)
			cmd.SetErr(stderr)
			cmd.SetArgs(createArgs)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stderr.String(), hint) != tc.want || strings.Contains(stdout.String(), hint) {
				t.Fatalf("stderr = %q, stdout = %q; want the hint on stderr: %v", stderr, stdout, tc.want)
			}
		})
	}
}
