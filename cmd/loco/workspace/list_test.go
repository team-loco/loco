package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/cmd/internal/session"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
)

const (
	testListUserID     = "00000000-0000-7000-8000-000000000003"
	testFirstWorkspace = "first"
	listUnavailable    = "workspace listing unavailable"
)

type workspacePage func(ctx context.Context, cursor string) ([]*workspacev1.Workspace, string, error)

type workspaceListServer struct {
	workspacev1connect.UnimplementedWorkspaceServiceHandler
	userv1connect.UnimplementedUserServiceHandler
	t           *testing.T
	orgID       string
	page        workspacePage
	mu          sync.Mutex
	whoCalls    int
	listCursors []string
}

func (s *workspaceListServer) WhoAmI(context.Context, *userv1.WhoAmIRequest) (*userv1.WhoAmIResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.whoCalls++
	return &userv1.WhoAmIResponse{User: &userv1.User{Id: testListUserID}}, nil
}

func (s *workspaceListServer) ListOrgWorkspaces(
	ctx context.Context,
	req *workspacev1.ListOrgWorkspacesRequest,
) (*workspacev1.ListOrgWorkspacesResponse, error) {
	if s.orgID == "" || req.GetOrgId() != s.orgID {
		s.t.Errorf("wrong organization scope: %q", req.GetOrgId())
	}
	rows, next, err := s.next(ctx, req.GetPageToken())
	return &workspacev1.ListOrgWorkspacesResponse{Workspaces: rows, NextPageToken: next}, err
}

func (s *workspaceListServer) ListUserWorkspaces(
	ctx context.Context,
	req *workspacev1.ListUserWorkspacesRequest,
) (*workspacev1.ListUserWorkspacesResponse, error) {
	if s.orgID != "" || req.GetUserId() != "" {
		s.t.Errorf("wrong user scope: %q", req.GetUserId())
	}
	if req.GetPageSize() != 0 {
		s.t.Errorf("page size = %d, want API default", req.GetPageSize())
	}
	rows, next, err := s.next(ctx, req.GetPageToken())
	return &workspacev1.ListUserWorkspacesResponse{Workspaces: rows, NextPageToken: next}, err
}

func (s *workspaceListServer) next(ctx context.Context, cursor string) ([]*workspacev1.Workspace, string, error) {
	s.mu.Lock()
	s.listCursors = append(s.listCursors, cursor)
	s.mu.Unlock()
	return s.page(ctx, cursor)
}

func runWorkspaceList(
	ctx context.Context,
	t *testing.T,
	orgID string,
	page workspacePage,
) (string, *workspaceListServer, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(keychain.StoreEnvVar, "file")
	credential := rand.Text()
	fake := &workspaceListServer{t: t, orgID: orgID, page: page}
	server := connect.NewServer()
	workspacev1connect.RegisterWorkspaceServiceHandler(server, fake)
	userv1connect.RegisterUserServiceHandler(server, fake)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	endpoint := httptest.NewServer(mux)
	t.Cleanup(endpoint.Close)
	t.Setenv("LOCO_HOST", endpoint.URL)
	store, err := keychain.ForCurrentUser()
	if err != nil {
		t.Fatal(err)
	}
	err = store.Set(keychain.UserToken{Host: endpoint.URL, Token: credential, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd := buildListCmd()
	cmd.SetOut(&output)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	args := []string{}
	if orgID != "" {
		args = append(args, "--org-id", orgID)
	}
	cmd.SetArgs(args)
	err = cmd.ExecuteContext(ctx)
	return output.String(), fake, err
}

func TestListWorkspacesFollowsOpaquePages(t *testing.T) {
	for _, orgID := range []string{"", testOrgID} {
		t.Run(orgID, func(t *testing.T) {
			ctx := t.Context()
			output, fake, err := runWorkspaceList(
				ctx,
				t,
				orgID,
				func(_ context.Context, cursor string) ([]*workspacev1.Workspace, string, error) {
					switch cursor {
					case "":
						return []*workspacev1.Workspace{{Id: testFirstWorkspace, Name: "One"}}, "opaque/cursor", nil
					case "opaque/cursor":
						return nil, "empty-page/cursor", nil
					case "empty-page/cursor":
						return []*workspacev1.Workspace{{Id: "last", Name: "Two", OrgId: testOrgID}}, "", nil
					default:
						t.Errorf("unexpected cursor %q", cursor)
						return nil, "", nil
					}
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			want := "Workspaces:\n  - One (ID: first)\n  - Two (ID: last)\n"
			if output != want {
				t.Fatalf("output = %q, want %q", output, want)
			}
			if len(fake.listCursors) != 3 {
				t.Fatalf("requested %d pages, want 3", len(fake.listCursors))
			}
			if fake.whoCalls != 0 {
				t.Fatalf("WhoAmI calls = %d, want 0", fake.whoCalls)
			}
			if orgID == "" {
				id, resolveErr := resolveListedWorkspace(t)
				if resolveErr != nil || id != "last" {
					t.Fatalf("resolve listed workspace: id=%q error=%v", id, resolveErr)
				}
				if len(fake.listCursors) != 6 {
					t.Fatalf("list and resolve requested %d pages, want 6", len(fake.listCursors))
				}
			}
		})
	}
}

func resolveListedWorkspace(t *testing.T) (string, error) {
	t.Helper()
	cmd := buildListCmd()
	target, err := cmdutil.ResolveLogin(cmd)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Flags().String("org", testOrgID, "")
	cmd.Flags().String("workspace", "Two", "")
	load := func() (*session.SessionConfig, error) { return session.NewSessionConfig(), nil }
	return cmdutil.ResolveWorkspaceID(t.Context(), cmd, load, target.Clients.Workspace)
}

func TestListWorkspacesRejectsPartialResults(t *testing.T) {
	for _, orgID := range []string{"", testOrgID} {
		t.Run(orgID, func(t *testing.T) {
			ctx := t.Context()
			output, _, err := runWorkspaceList(
				ctx,
				t,
				orgID,
				func(_ context.Context, cursor string) ([]*workspacev1.Workspace, string, error) {
					if cursor == "" {
						return []*workspacev1.Workspace{{Id: testFirstWorkspace}}, "next", nil
					}
					return nil, "", connect.NewError(connect.CodeUnavailable, listUnavailable)
				},
			)
			if err == nil || !strings.Contains(err.Error(), listUnavailable) || output != "" {
				t.Fatalf("partial listing = %q, error = %v", output, err)
			}
		})
	}
}

func TestListWorkspacesRejectsRepeatedPages(t *testing.T) {
	for _, orgID := range []string{"", testOrgID} {
		t.Run(orgID, func(t *testing.T) {
			ctx := t.Context()
			output, fake, err := runWorkspaceList(
				ctx,
				t,
				orgID,
				func(context.Context, string) ([]*workspacev1.Workspace, string, error) {
					return nil, "repeated", nil
				},
			)
			if !errors.Is(err, cmdutil.ErrRepeatedPageToken) || output != "" || len(fake.listCursors) != 2 {
				t.Fatalf("repeated page: output=%q error=%v calls=%d", output, err, len(fake.listCursors))
			}
			if orgID == "" {
				_, resolveErr := resolveListedWorkspace(t)
				if !errors.Is(resolveErr, cmdutil.ErrRepeatedPageToken) || len(fake.listCursors) != 4 {
					t.Fatalf("resolve repeated page: error=%v calls=%d", resolveErr, len(fake.listCursors))
				}
			}
		})
	}
}

func TestListWorkspacesForwardsCancellation(t *testing.T) {
	for _, orgID := range []string{"", testOrgID} {
		t.Run(orgID, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			output, fake, err := runWorkspaceList(
				ctx,
				t,
				orgID,
				func(context.Context, string) ([]*workspacev1.Workspace, string, error) {
					cancel()
					return nil, "next", nil
				},
			)
			if !errors.Is(err, context.Canceled) || output != "" || len(fake.listCursors) != 1 {
				t.Fatalf("canceled listing: output=%q error=%v calls=%d", output, err, len(fake.listCursors))
			}
		})
	}
}
