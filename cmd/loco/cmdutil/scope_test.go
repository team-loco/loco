package cmdutil

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/spf13/cobra"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/rpc"
	"github.com/team-loco/loco/internal/session"
)

func TestResolveWorkspaceIDIgnoresCachedWorkspaceOfAnotherOrg(t *testing.T) {
	loadConfig := func() (*session.SessionConfig, error) {
		cfg := session.NewSessionConfig()
		cfg.Scopes[session.DefaultScope] = &session.Scope{
			Organization: session.SimpleOrg{ID: "org-a-id", Name: "org-a"},
			Workspace:    session.SimpleWorkspace{ID: "org-a-default-id", Name: "default"},
		}
		return cfg, nil
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("org", "", "")
	cmd.Flags().String("workspace", "", "")
	if err := cmd.Flags().Set("org", "org-b"); err != nil {
		t.Fatalf("set org flag: %v", err)
	}

	id, err := ResolveWorkspaceID(context.Background(), cmd, loadConfig, nil)
	if id == "org-a-default-id" {
		t.Fatalf("resolved org-b's default workspace to org-a's cached workspace ID")
	}
	if err == nil {
		t.Fatalf("expected an error resolving org-b without an API client, got id %q", id)
	}
}

func TestResolveOrgAndWorkspaceReportUnreadableConfig(t *testing.T) {
	t.Setenv("LOCO_ORG", "")
	t.Setenv("LOCO_WORKSPACE", "")
	loadErr := errors.New("decode failure")
	loadConfig := func() (*session.SessionConfig, error) { return nil, loadErr }

	cmd := &cobra.Command{}
	cmd.Flags().String("org", "", "")
	cmd.Flags().String("workspace", "", "")

	if _, err := ResolveOrg(cmd, loadConfig); !errors.Is(err, loadErr) {
		t.Fatalf("ResolveOrg error = %v, want it to wrap %v", err, loadErr)
	}
	if _, err := ResolveWorkspace(cmd, loadConfig); !errors.Is(err, loadErr) {
		t.Fatalf("ResolveWorkspace error = %v, want it to wrap %v", err, loadErr)
	}
}

type pagedWorkspaces struct {
	workspacev1connect.UnimplementedWorkspaceServiceHandler
}

func (pagedWorkspaces) ListUserWorkspaces(
	_ context.Context,
	req *workspacev1.ListUserWorkspacesRequest,
) (*workspacev1.ListUserWorkspacesResponse, error) {
	if req.GetPageToken() == "" {
		return &workspacev1.ListUserWorkspacesResponse{
			Workspaces:    []*workspacev1.Workspace{{Id: "ws-1", Name: "first", OrgName: "acme"}},
			NextPageToken: "page-2",
		}, nil
	}
	return &workspacev1.ListUserWorkspacesResponse{
		Workspaces: []*workspacev1.Workspace{{Id: "ws-2", Name: "second", OrgName: "acme"}},
	}, nil
}

func TestResolveWorkspaceIDFindsWorkspaceOnLaterPage(t *testing.T) {
	t.Setenv("LOCO_ORG", "")
	t.Setenv("LOCO_WORKSPACE", "")
	server := connect.NewServer()
	workspacev1connect.RegisterWorkspaceServiceHandler(server, pagedWorkspaces{})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	apiClient := &client.Client{Workspace: rpc.NewHTTPClients(srv.Client(), srv.URL, "").Workspace}
	loadConfig := func() (*session.SessionConfig, error) { return session.NewSessionConfig(), nil }
	cmd := &cobra.Command{}
	cmd.Flags().String("org", "acme", "")
	cmd.Flags().String("workspace", "second", "")

	id, err := ResolveWorkspaceID(context.Background(), cmd, loadConfig, apiClient)
	if err != nil {
		t.Fatalf("ResolveWorkspaceID: %v", err)
	}
	if id != "ws-2" {
		t.Fatalf("workspace id = %q, want ws-2", id)
	}
}
