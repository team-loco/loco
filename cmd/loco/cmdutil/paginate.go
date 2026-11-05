package cmdutil

import (
	"context"
	"errors"
	"fmt"

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

const workspacePageSize = 100

func ListUserWorkspaces(
	ctx context.Context,
	workspaces workspacev1connect.WorkspaceServiceClient,
) ([]*workspacev1.Workspace, error) {
	return Paginate(func(token string) ([]*workspacev1.Workspace, string, error) {
		resp, err := workspaces.ListUserWorkspaces(ctx, &workspacev1.ListUserWorkspacesRequest{
			PageSize:  workspacePageSize,
			PageToken: token,
		})
		if err != nil {
			return nil, "", err
		}
		return resp.GetWorkspaces(), resp.GetNextPageToken(), nil
	})
}
