package workspace

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/internal/rpc"
)

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

			target, err := cmdutil.ResolveLogin(cmd)
			if err != nil {
				return err
			}

			orgID, err := cmd.Flags().GetString("org-id")
			if err != nil {
				return fmt.Errorf("failed to get org-id flag: %w", err)
			}

			workspaces, err := listWorkspaces(ctx, target.Clients, orgID)
			if err != nil {
				return err
			}
			if len(workspaces) == 0 {
				message := "No workspaces found."
				if orgID != "" {
					message = "No workspaces found in this organization."
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), message)
				return err
			}
			if _, err = fmt.Fprintln(cmd.OutOrStdout(), "Workspaces:"); err != nil {
				return err
			}
			for _, workspace := range workspaces {
				if _, err = fmt.Fprintf(
					cmd.OutOrStdout(),
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

func listWorkspaces(ctx context.Context, clients *rpc.Clients, orgID string) ([]*workspacev1.Workspace, error) {
	var workspaces []*workspacev1.Workspace
	var err error
	if orgID == "" {
		workspaces, err = cmdutil.ListUserWorkspaces(ctx, clients.Workspace)
	} else {
		workspaces, err = cmdutil.Paginate(func(token string) ([]*workspacev1.Workspace, string, error) {
			request := &workspacev1.ListOrgWorkspacesRequest{OrgId: orgID, PageToken: token}
			response, listErr := clients.Workspace.ListOrgWorkspaces(ctx, request)
			if listErr != nil {
				return nil, "", listErr
			}
			return response.GetWorkspaces(), response.GetNextPageToken(), nil
		})
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}
	return workspaces, nil
}
