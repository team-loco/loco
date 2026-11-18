package workspace

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/team-loco/loco/cmd/internal/clitest"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	testUpdateAction = "update"
	testOrgID        = "00000000-0000-7000-8000-000000000001"
	testWorkspaceID  = "00000000-0000-7000-8000-000000000002"
)

// testOrgs serves the caller's single organization.
type testOrgs struct {
	userv1connect.UnimplementedUserServiceHandler
	orgv1connect.UnimplementedOrgServiceHandler
}

func (testOrgs) WhoAmI(context.Context, *userv1.WhoAmIRequest) (*userv1.WhoAmIResponse, error) {
	return &userv1.WhoAmIResponse{User: &userv1.User{Id: "user"}}, nil
}

func (testOrgs) ListUserOrgs(context.Context, *orgv1.ListUserOrgsRequest) (*orgv1.ListUserOrgsResponse, error) {
	return &orgv1.ListUserOrgsResponse{Orgs: []*orgv1.Organization{{Id: testOrgID, Name: "acme"}}}, nil
}

func (o testOrgs) register(server *connect.Server) {
	userv1connect.RegisterUserServiceHandler(server, o)
	orgv1connect.RegisterOrgServiceHandler(server, o)
}

type workspaceUpdateServer struct {
	workspacev1connect.UnimplementedWorkspaceServiceHandler
	check   func(*workspacev1.UpdateWorkspaceRequest)
	updated bool
}

func (*workspaceUpdateServer) ListOrgWorkspaces(
	context.Context,
	*workspacev1.ListOrgWorkspacesRequest,
) (*workspacev1.ListOrgWorkspacesResponse, error) {
	return &workspacev1.ListOrgWorkspacesResponse{
		Workspaces: []*workspacev1.Workspace{{Id: testWorkspaceID, OrgId: testOrgID, Name: "example"}},
	}, nil
}

func (s *workspaceUpdateServer) UpdateWorkspace(
	_ context.Context,
	req *workspacev1.UpdateWorkspaceRequest,
) (*workspacev1.UpdateWorkspaceResponse, error) {
	s.check(req)
	s.updated = true
	return &workspacev1.UpdateWorkspaceResponse{}, nil
}

func TestWorkspaceUpdatePreservesExplicitEmptyFlags(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		field string
	}{
		{"clear description", []string{"--description", ""}, "description"},
		{"explicit empty name", []string{"--name", ""}, "name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &workspaceUpdateServer{
				check: func(req *workspacev1.UpdateWorkspaceRequest) {
					if req.GetWorkspaceId() != testWorkspaceID {
						t.Errorf("workspace ID = %q", req.GetWorkspaceId())
					}
					message := req.ProtoReflect()
					fields := message.Descriptor().Fields()
					for _, name := range []protoreflect.Name{"name", "description"} {
						field := fields.ByName(name)
						if got := message.Has(field); got != (string(name) == tc.field) {
							t.Errorf("%s present=%v, want %v", name, got, string(name) == tc.field)
						}
						if message.Get(field).String() != "" {
							t.Errorf("%s was not empty", name)
						}
					}
				},
			}
			clitest.Session(t, func(server *connect.Server) {
				testOrgs{}.register(server)
				workspacev1connect.RegisterWorkspaceServiceHandler(server, fake)
			})
			cmd := BuildWorkspaceCmd()
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			args := append([]string{testUpdateAction, "example", "--org", "acme"}, tc.args...)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if !fake.updated {
				t.Fatal("workspace was not updated")
			}
		})
	}
}

func TestWorkspaceUpdateRequiresAnExplicitFlag(t *testing.T) {
	cmd := BuildWorkspaceCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{testUpdateAction, testWorkspaceID})
	if err := cmd.Execute(); !errors.Is(err, errNoUpdateFlags) {
		t.Fatalf("update without flags error=%v", err)
	}
}
