package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/team-loco/loco/internal/rpc"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

var errRepeatedWorkspacePage = errors.New("workspace listing returned a repeated page")

type listDeps struct {
	WhoAmI func(
		ctx context.Context,
		req *userv1.WhoAmIRequest,
	) (*userv1.WhoAmIResponse, error)
	ListUserWorkspaces func(
		ctx context.Context,
		req *workspacev1.ListUserWorkspacesRequest,
	) (*workspacev1.ListUserWorkspacesResponse, error)
	ListOrgWorkspaces func(
		ctx context.Context,
		req *workspacev1.ListOrgWorkspacesRequest,
	) (*workspacev1.ListOrgWorkspacesResponse, error)
	Output io.Writer
}

func buildListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List workspaces",
		Long:  "List all workspaces you have access to.",
		Args:  cobra.NoArgs,
		Example: `  # List all your workspaces
  loco workspace list

  # List workspaces in a specific organization
  loco workspace list --org-id <organization-id>`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}
			locoToken, err := cmdutil.GetCurrentLocoToken(cmd)
			if err != nil {
				return err
			}

			clients := rpc.ForHost(host, locoToken.Token)
			wsClient := clients.Workspace
			userClient := clients.User

			deps := listDeps{
				WhoAmI:             userClient.WhoAmI,
				ListUserWorkspaces: wsClient.ListUserWorkspaces,
				ListOrgWorkspaces:  wsClient.ListOrgWorkspaces,
				Output:             os.Stdout,
			}

			orgID, err := cmd.Flags().GetString("org-id")
			if err != nil {
				return fmt.Errorf("failed to get org-id flag: %w", err)
			}

			workspaces, err := listWorkspaces(ctx, deps, orgID)
			if err != nil {
				return err
			}
			if len(workspaces) == 0 {
				message := "No workspaces found."
				if orgID != "" {
					message = "No workspaces found in this organization."
				}
				_, err = fmt.Fprintln(deps.Output, message)
				return err
			}
			if _, err = fmt.Fprintln(deps.Output, "Workspaces:"); err != nil {
				return err
			}
			for _, workspace := range workspaces {
				if _, err = fmt.Fprintf(
					deps.Output,
					"  - %s (ID: %s)\n",
					workspace.GetName(),
					workspace.GetId(),
				); err != nil {
					return err
				}
			}

			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().String("org-id", "", "Filter by organization ID")

	return cmd
}

func listWorkspaces(ctx context.Context, deps listDeps, orgID string) ([]*workspacev1.Workspace, error) {
	var userID string
	if orgID == "" {
		who, err := deps.WhoAmI(ctx, &userv1.WhoAmIRequest{})
		if err != nil {
			return nil, fmt.Errorf("failed to get current user: %w", err)
		}
		userID = who.GetUser().GetId()
	}
	var workspaces []*workspacev1.Workspace
	var pageToken string
	seen := make(map[string]struct{})
	for {
		var next string
		if orgID != "" {
			request := &workspacev1.ListOrgWorkspacesRequest{OrgId: orgID, PageToken: pageToken}
			response, err := deps.ListOrgWorkspaces(ctx, request)
			if err != nil {
				return nil, fmt.Errorf("failed to list workspaces: %w", err)
			}
			workspaces = append(workspaces, response.GetWorkspaces()...)
			next = response.GetNextPageToken()
		} else {
			request := &workspacev1.ListUserWorkspacesRequest{UserId: userID, PageToken: pageToken}
			response, err := deps.ListUserWorkspaces(ctx, request)
			if err != nil {
				return nil, fmt.Errorf("failed to list workspaces: %w", err)
			}
			workspaces = append(workspaces, response.GetWorkspaces()...)
			next = response.GetNextPageToken()
		}
		if next == "" {
			return workspaces, nil
		}
		if _, duplicate := seen[next]; duplicate {
			return nil, errRepeatedWorkspacePage
		}
		seen[next] = struct{}{}
		pageToken = next
	}
}
