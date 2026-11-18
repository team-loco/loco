package cmdutil

import (
	"context"
	"fmt"

	"github.com/team-loco/loco/cmd/internal/paginate"

	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
)

func ListUserWorkspaces(
	ctx context.Context,
	workspaces workspacev1connect.WorkspaceServiceClient,
) ([]*workspacev1.Workspace, error) {
	return paginate.All(func(token string) ([]*workspacev1.Workspace, string, error) {
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
	return paginate.All(func(token string) ([]*orgv1.Organization, string, error) {
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
