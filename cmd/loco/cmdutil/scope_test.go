package cmdutil

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/spf13/cobra"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
	"github.com/team-loco/loco/internal/rpc"
	"github.com/team-loco/loco/internal/session"
)

const (
	testOrgName           = "acme"
	testFirstOrgID        = "org-1"
	testSecondOrgID       = "org-2"
	testFirstWorkspaceID  = "ws-1"
	testWorkspaceName     = "default"
	testSecondWorkspaceID = "ws-2"
)

func TestResolveWorkspaceIDIgnoresCachedWorkspaceOfAnotherOrg(t *testing.T) {
	loadConfig := func() (*session.SessionConfig, error) {
		cfg := session.NewSessionConfig()
		cfg.Scopes[session.DefaultScope] = &session.Scope{
			Organization: session.SimpleOrg{ID: "org-a-id", Name: "org-a"},
			Workspace:    session.SimpleWorkspace{ID: "org-a-default-id", Name: testWorkspaceName},
		}
		return cfg, nil
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("org", "", "")
	cmd.Flags().String("workspace", "", "")
	if err := cmd.Flags().Set("org", "org-b"); err != nil {
		t.Fatalf("set org flag: %v", err)
	}

	server := connect.NewServer()
	workspacev1connect.RegisterWorkspaceServiceHandler(server, pagedWorkspaces{})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	workspaceClient := rpc.NewHTTPClients(srv.Client(), srv.URL, "").Workspace
	id, err := ResolveWorkspaceID(context.Background(), cmd, loadConfig, workspaceClient)
	if id == "org-a-default-id" {
		t.Fatalf("resolved org-b's default workspace to org-a's cached workspace ID")
	}
	if err == nil {
		t.Fatalf("expected an error resolving org-b, which has no workspaces, got id %q", id)
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
			Workspaces:    []*workspacev1.Workspace{{Id: testFirstWorkspaceID, Name: "first", OrgName: testOrgName}},
			NextPageToken: "page-2",
		}, nil
	}
	return &workspacev1.ListUserWorkspacesResponse{
		Workspaces: []*workspacev1.Workspace{{Id: testSecondWorkspaceID, Name: "second", OrgName: testOrgName}},
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

	workspaceClient := rpc.NewHTTPClients(srv.Client(), srv.URL, "").Workspace
	loadConfig := func() (*session.SessionConfig, error) { return session.NewSessionConfig(), nil }
	cmd := &cobra.Command{}
	cmd.Flags().String("org", testOrgName, "")
	cmd.Flags().String("workspace", "second", "")

	id, err := ResolveWorkspaceID(context.Background(), cmd, loadConfig, workspaceClient)
	if err != nil {
		t.Fatalf("ResolveWorkspaceID: %v", err)
	}
	if id != testSecondWorkspaceID {
		t.Fatalf("workspace id = %q, want ws-2", id)
	}
}

func TestFindOrgRejectsASharedNameAndAcceptsTheID(t *testing.T) {
	orgs := []*orgv1.Organization{{Id: testFirstOrgID, Name: testOrgName}, {Id: testSecondOrgID, Name: testOrgName}}

	if _, err := FindOrg(orgs, testOrgName); err == nil || !strings.Contains(err.Error(), "matches 2 organizations") {
		t.Fatalf("FindOrg(acme) error = %v, want it to report 2 matches", err)
	}
	org, err := FindOrg(orgs, testSecondOrgID)
	if err != nil {
		t.Fatalf("FindOrg(org-2): %v", err)
	}
	if org.GetId() != testSecondOrgID {
		t.Fatalf("org = %q, want org-2", org.GetId())
	}
}

func TestFindWorkspaceRejectsASharedNameAndAcceptsTheID(t *testing.T) {
	workspaces := []*workspacev1.Workspace{
		{Id: testFirstWorkspaceID, Name: testWorkspaceName, OrgId: testFirstOrgID, OrgName: testOrgName},
		{Id: testSecondWorkspaceID, Name: testWorkspaceName, OrgId: testSecondOrgID, OrgName: testOrgName},
		{Id: "ws-3", Name: testWorkspaceName, OrgId: "org-3", OrgName: "other"},
	}

	_, err := FindWorkspace(workspaces, testOrgName, testWorkspaceName)
	if err == nil || !strings.Contains(err.Error(), "matches 2 workspaces") {
		t.Fatalf("FindWorkspace(acme, default) error = %v, want it to report 2 matches", err)
	}
	for _, org := range []string{testOrgName, testSecondOrgID} {
		ws, findErr := FindWorkspace(workspaces, org, testSecondWorkspaceID)
		if findErr != nil {
			t.Fatalf("FindWorkspace(%s, ws-2): %v", org, findErr)
		}
		if ws.GetId() != testSecondWorkspaceID {
			t.Fatalf("workspace = %q, want ws-2", ws.GetId())
		}
	}
	ws, err := FindWorkspace(workspaces, testFirstOrgID, testWorkspaceName)
	if err != nil {
		t.Fatalf("FindWorkspace(org-1, default): %v", err)
	}
	if ws.GetId() != testFirstWorkspaceID {
		t.Fatalf("workspace = %q, want ws-1", ws.GetId())
	}
}

type sharedNameWorkspaces struct {
	workspacev1connect.UnimplementedWorkspaceServiceHandler
}

func (sharedNameWorkspaces) ListUserWorkspaces(
	context.Context,
	*workspacev1.ListUserWorkspacesRequest,
) (*workspacev1.ListUserWorkspacesResponse, error) {
	return &workspacev1.ListUserWorkspacesResponse{Workspaces: []*workspacev1.Workspace{
		{Id: testSecondWorkspaceID, Name: "staging", OrgId: testSecondOrgID, OrgName: testOrgName},
		{Id: testFirstWorkspaceID, Name: "staging", OrgId: testFirstOrgID, OrgName: testOrgName},
	}}, nil
}

func TestResolveWorkspaceIDUsesTheSavedOrgID(t *testing.T) {
	t.Setenv("LOCO_ORG", "")
	t.Setenv("LOCO_WORKSPACE", "")
	server := connect.NewServer()
	workspacev1connect.RegisterWorkspaceServiceHandler(server, sharedNameWorkspaces{})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	workspaceClient := rpc.NewHTTPClients(srv.Client(), srv.URL, "").Workspace
	loadConfig := func() (*session.SessionConfig, error) {
		cfg := session.NewSessionConfig()
		cfg.Scopes[session.DefaultScope] = &session.Scope{
			Organization: session.SimpleOrg{ID: testFirstOrgID, Name: testOrgName},
			Workspace:    session.SimpleWorkspace{ID: "ws-0", Name: testWorkspaceName},
		}
		return cfg, nil
	}
	cmd := &cobra.Command{}
	cmd.Flags().String("org", "", "")
	cmd.Flags().String("workspace", "staging", "")

	id, err := ResolveWorkspaceID(context.Background(), cmd, loadConfig, workspaceClient)
	if err != nil {
		t.Fatalf("ResolveWorkspaceID: %v", err)
	}
	if id != testFirstWorkspaceID {
		t.Fatalf("workspace id = %q, want ws-1 in the saved org-1", id)
	}
}
