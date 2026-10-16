package workspace

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
	"github.com/team-loco/loco/internal/keychain"
)

const (
	testListAction    = "list"
	testCreateAction  = "create"
	testUpdateAction  = "update"
	testDeleteAction  = "delete"
	testWorkspaceName = "example"
	testOrgID         = "00000000-0000-7000-8000-000000000001"
	testWorkspaceID   = "00000000-0000-7000-8000-000000000002"
)

type workspaceIDsServer struct {
	workspacev1connect.UnimplementedWorkspaceServiceHandler
	t          *testing.T
	calls      []string
	credential string
}

func (s *workspaceIDsServer) record(ctx context.Context, method, got, want string) {
	s.t.Helper()
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok || info.RequestHeader().Get("Authorization") != "Bearer "+s.credential {
		s.t.Error("workspace request is not authenticated")
	}
	if got != want {
		s.t.Errorf("%s ID = %q, want %q", method, got, want)
	}
	s.calls = append(s.calls, method)
}

func (s *workspaceIDsServer) ListOrgWorkspaces(
	ctx context.Context,
	req *workspacev1.ListOrgWorkspacesRequest,
) (*workspacev1.ListOrgWorkspacesResponse, error) {
	s.record(ctx, testListAction, req.GetOrgId(), testOrgID)
	return &workspacev1.ListOrgWorkspacesResponse{
		Workspaces: []*workspacev1.Workspace{{Id: testWorkspaceID, OrgId: testOrgID, Name: testWorkspaceName}},
	}, nil
}

func (s *workspaceIDsServer) CreateWorkspace(
	ctx context.Context,
	req *workspacev1.CreateWorkspaceRequest,
) (*workspacev1.CreateWorkspaceResponse, error) {
	s.record(ctx, testCreateAction, req.GetOrgId(), testOrgID)
	if req.GetName() != testWorkspaceName || req.GetDescription() != "Example workspace" {
		s.t.Errorf("create request = %v", req)
	}
	return &workspacev1.CreateWorkspaceResponse{WorkspaceId: testWorkspaceID}, nil
}

func (s *workspaceIDsServer) UpdateWorkspace(
	ctx context.Context,
	req *workspacev1.UpdateWorkspaceRequest,
) (*workspacev1.UpdateWorkspaceResponse, error) {
	s.record(ctx, testUpdateAction, req.GetWorkspaceId(), testWorkspaceID)
	if req.GetName() != "renamed" || !slices.Equal(req.GetUpdateMask().GetPaths(), []string{"name"}) {
		s.t.Errorf("update request = %v", req)
	}
	return &workspacev1.UpdateWorkspaceResponse{}, nil
}

func (s *workspaceIDsServer) GetWorkspace(
	ctx context.Context,
	req *workspacev1.GetWorkspaceRequest,
) (*workspacev1.GetWorkspaceResponse, error) {
	s.record(ctx, "get", req.GetWorkspaceId(), testWorkspaceID)
	return &workspacev1.GetWorkspaceResponse{
		Workspace: &workspacev1.Workspace{Id: testWorkspaceID, Name: testWorkspaceName},
	}, nil
}

func (s *workspaceIDsServer) DeleteWorkspace(
	ctx context.Context,
	req *workspacev1.DeleteWorkspaceRequest,
) (*workspacev1.DeleteWorkspaceResponse, error) {
	s.record(ctx, testDeleteAction, req.GetWorkspaceId(), testWorkspaceID)
	if !req.GetConfirmDeleteApps() {
		s.t.Error("explicit app deletion confirmation was lost")
	}
	return &workspacev1.DeleteWorkspaceResponse{}, nil
}

func TestWorkspaceCommandsUseUUIDs(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		calls []string
	}{
		{name: testListAction, args: []string{testListAction, "--org-id", testOrgID}, calls: []string{testListAction}},
		{
			name: testCreateAction,
			args: []string{
				testCreateAction,
				testWorkspaceName,
				"--org-id",
				testOrgID,
				"--description",
				"Example workspace",
			},
			calls: []string{testCreateAction},
		},
		{
			name:  testUpdateAction,
			args:  []string{testUpdateAction, testWorkspaceID, "--name", "renamed"},
			calls: []string{testUpdateAction},
		},
		{
			name:  testDeleteAction,
			args:  []string{testDeleteAction, testWorkspaceID, "--yes", "--confirm-delete-apps"},
			calls: []string{"get", testDeleteAction},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv(keychain.StoreEnvVar, "file")
			credential := rand.Text()
			fake := &workspaceIDsServer{t: t, credential: credential}
			server := connect.NewServer()
			workspacev1connect.RegisterWorkspaceServiceHandler(server, fake)
			mux := http.NewServeMux()
			connecthttp.Mount(mux, server)
			endpoint := httptest.NewServer(mux)
			t.Cleanup(endpoint.Close)
			t.Setenv("LOCO_HOST", endpoint.URL)
			store, err := keychain.ForCurrentUser()
			if err != nil {
				t.Fatal(err)
			}
			err = store.Set(
				keychain.UserToken{Host: endpoint.URL, Token: credential, ExpiresAt: time.Now().Add(time.Hour)},
			)
			if err != nil {
				t.Fatal(err)
			}
			cmd := BuildWorkspaceCmd()
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(fake.calls, tc.calls) {
				t.Fatalf("RPC calls = %v, want %v", fake.calls, tc.calls)
			}
		})
	}
}
