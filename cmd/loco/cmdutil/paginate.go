package cmdutil

import (
	"context"
	"errors"
	"fmt"

	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
)

var ErrRepeatedPageToken = errors.New("listing returned a repeated page token")

// Paginate calls fetch with each page token until it returns an empty next
// token, so a client never reads only the first page of a list RPC.
func Paginate[T any](fetch func(token string) ([]T, string, error)) ([]T, error) {
	var all []T
	var token string
	seen := make(map[string]struct{})
	for {
		items, next, err := fetch(token)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if next == "" {
			return all, nil
		}
		if _, duplicate := seen[next]; duplicate {
			return nil, fmt.Errorf("%w: %q", ErrRepeatedPageToken, next)
		}
		seen[next] = struct{}{}
		token = next
	}
}

func ListUserWorkspaces(
	ctx context.Context,
	workspaces workspacev1connect.WorkspaceServiceClient,
) ([]*workspacev1.Workspace, error) {
	return Paginate(func(token string) ([]*workspacev1.Workspace, string, error) {
		resp, err := workspaces.ListUserWorkspaces(ctx, &workspacev1.ListUserWorkspacesRequest{
			PageToken: token,
		})
		if err != nil {
			return nil, "", err
		}
		return resp.GetWorkspaces(), resp.GetNextPageToken(), nil
	})
}

// ListUserOrgs lists every organization the caller belongs to.
func ListUserOrgs(
	ctx context.Context,
	users userv1connect.UserServiceClient,
	orgs orgv1connect.OrgServiceClient,
) ([]*orgv1.Organization, error) {
	me, err := users.WhoAmI(ctx, &userv1.WhoAmIRequest{})
	if err != nil {
		return nil, fmt.Errorf("get current user: %w", err)
	}
	return Paginate(func(token string) ([]*orgv1.Organization, string, error) {
		resp, err := orgs.ListUserOrgs(ctx, &orgv1.ListUserOrgsRequest{
			UserId:    me.GetUser().GetId(),
			PageToken: token,
		})
		if err != nil {
			return nil, "", err
		}
		return resp.GetOrgs(), resp.GetNextPageToken(), nil
	})
}
