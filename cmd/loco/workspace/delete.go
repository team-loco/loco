package workspace

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func buildDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <workspace-id>",
		Short: "Delete a workspace",
		Long:  "Delete a workspace by ID. This action cannot be undone.",
		Args:  cobra.ExactArgs(1),
		Example: `  # Delete a workspace (with confirmation prompt)
  loco workspace delete <workspace-id>

  # Delete a workspace without confirmation
  loco workspace delete <workspace-id> --yes

  # Delete a workspace and all its apps
  loco workspace delete <workspace-id> --yes --confirm-delete-apps`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			id := args[0]

			target, err := cmdutil.ResolveLogin(cmd)
			if err != nil {
				return err
			}

			wsClient := target.Clients.Workspace

			getReq := &workspacev1.GetWorkspaceRequest{
				WorkspaceId: id,
			}

			getResp, err := wsClient.GetWorkspace(ctx, getReq)
			if err != nil {
				return fmt.Errorf("failed to find workspace: %w", err)
			}

			confirmDeleteApps, err := cmd.Flags().GetBool("confirm-delete-apps")
			if err != nil {
				return fmt.Errorf("failed to get confirm-delete-apps flag: %w", err)
			}

			confirmed, err := cmdutil.Confirm(cmd, fmt.Sprintf(
				"Are you sure you want to delete workspace %q (ID: %s)? This cannot be undone.",
				getResp.GetWorkspace().GetName(),
				id,
			))
			if err != nil || !confirmed {
				return err
			}

			delReq := &workspacev1.DeleteWorkspaceRequest{
				WorkspaceId:       id,
				ConfirmDeleteApps: confirmDeleteApps,
			}

			_, err = wsClient.DeleteWorkspace(ctx, delReq)
			if err != nil {
				return fmt.Errorf("failed to delete workspace: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Workspace %q deleted successfully.\n", getResp.GetWorkspace().GetName())
			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().Bool("confirm-delete-apps", false, "Confirm deletion of all apps in the workspace")

	return cmd
}
