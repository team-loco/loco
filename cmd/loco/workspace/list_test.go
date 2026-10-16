package workspace

import (
	"context"
	"errors"
	"testing"

	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

const (
	testListUserID     = "00000000-0000-7000-8000-000000000003"
	testFirstWorkspace = "first"
)

var errListUnavailable = errors.New("workspace listing unavailable")

func workspaceListDeps(
	t *testing.T,
	orgID string,
	page func(context.Context, string) ([]*workspacev1.Workspace, string, error),
) (listDeps, *int) {
	t.Helper()
	whoCalls := new(int)
	return listDeps{
		WhoAmI: func(context.Context, *userv1.WhoAmIRequest) (*userv1.WhoAmIResponse, error) {
			*whoCalls++
			return &userv1.WhoAmIResponse{User: &userv1.User{Id: testListUserID}}, nil
		},
		ListOrgWorkspaces: func(
			ctx context.Context,
			req *workspacev1.ListOrgWorkspacesRequest,
		) (*workspacev1.ListOrgWorkspacesResponse, error) {
			if orgID == "" || req.GetOrgId() != orgID {
				t.Errorf("wrong organization scope: %q", req.GetOrgId())
			}
			rows, next, err := page(ctx, req.GetPageToken())
			return &workspacev1.ListOrgWorkspacesResponse{Workspaces: rows, NextPageToken: next}, err
		},
		ListUserWorkspaces: func(
			ctx context.Context,
			req *workspacev1.ListUserWorkspacesRequest,
		) (*workspacev1.ListUserWorkspacesResponse, error) {
			if orgID != "" || req.GetUserId() != testListUserID {
				t.Errorf("wrong user scope: %q", req.GetUserId())
			}
			rows, next, err := page(ctx, req.GetPageToken())
			return &workspacev1.ListUserWorkspacesResponse{Workspaces: rows, NextPageToken: next}, err
		},
	}, whoCalls
}

func TestListWorkspacesFollowsOpaquePages(t *testing.T) {
	for _, orgID := range []string{"", testOrgID} {
		t.Run(orgID, func(t *testing.T) {
			var cursors []string
			deps, whoCalls := workspaceListDeps(
				t,
				orgID,
				func(_ context.Context, cursor string) ([]*workspacev1.Workspace, string, error) {
					cursors = append(cursors, cursor)
					switch cursor {
					case "":
						return []*workspacev1.Workspace{{Id: testFirstWorkspace}}, "opaque/cursor", nil
					case "opaque/cursor":
						return nil, "empty-page/cursor", nil
					case "empty-page/cursor":
						return []*workspacev1.Workspace{{Id: "last"}}, "", nil
					default:
						t.Fatalf("unexpected cursor %q", cursor)
						return nil, "", nil
					}
				},
			)
			rows, err := listWorkspaces(t.Context(), deps, orgID)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 2 || rows[0].GetId() != testFirstWorkspace || rows[1].GetId() != "last" {
				t.Fatalf("incomplete workspace listing: %v", rows)
			}
			if len(cursors) != 3 {
				t.Fatalf("requested %d pages, want 3", len(cursors))
			}
			expectedWho := 0
			if orgID == "" {
				expectedWho = 1
			}
			if *whoCalls != expectedWho {
				t.Fatalf("WhoAmI calls = %d, want %d", *whoCalls, expectedWho)
			}
		})
	}
}

func TestListWorkspacesRejectsPartialResults(t *testing.T) {
	for _, orgID := range []string{"", testOrgID} {
		t.Run(orgID, func(t *testing.T) {
			deps, _ := workspaceListDeps(
				t,
				orgID,
				func(_ context.Context, cursor string) ([]*workspacev1.Workspace, string, error) {
					if cursor == "" {
						return []*workspacev1.Workspace{{Id: testFirstWorkspace}}, "next", nil
					}
					return nil, "", errListUnavailable
				},
			)
			rows, err := listWorkspaces(t.Context(), deps, orgID)
			if !errors.Is(err, errListUnavailable) || rows != nil {
				t.Fatalf("partial listing = %v, error = %v", rows, err)
			}
		})
	}
}

func TestListWorkspacesRejectsRepeatedPages(t *testing.T) {
	for _, orgID := range []string{"", testOrgID} {
		t.Run(orgID, func(t *testing.T) {
			calls := 0
			deps, _ := workspaceListDeps(
				t,
				orgID,
				func(_ context.Context, _ string) ([]*workspacev1.Workspace, string, error) {
					calls++
					return nil, "repeated", nil
				},
			)
			rows, err := listWorkspaces(t.Context(), deps, orgID)
			if !errors.Is(err, errRepeatedWorkspacePage) || rows != nil || calls != 2 {
				t.Fatalf("repeated page: rows=%v error=%v calls=%d", rows, err, calls)
			}
		})
	}
}

func TestListWorkspacesForwardsCancellation(t *testing.T) {
	for _, orgID := range []string{"", testOrgID} {
		t.Run(orgID, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			calls := 0
			deps, _ := workspaceListDeps(
				t,
				orgID,
				func(ctx context.Context, _ string) ([]*workspacev1.Workspace, string, error) {
					calls++
					if calls == 1 {
						cancel()
						return nil, "next", nil
					}
					return nil, "", ctx.Err()
				},
			)
			rows, err := listWorkspaces(ctx, deps, orgID)
			if !errors.Is(err, context.Canceled) || rows != nil || calls != 2 {
				t.Fatalf("canceled listing: rows=%v error=%v calls=%d", rows, err, calls)
			}
		})
	}
}
