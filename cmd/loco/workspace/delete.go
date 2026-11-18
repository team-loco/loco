package workspace

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func buildDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name-or-id>",
		Short: "Delete a workspace",
		Long:  "Delete a workspace by name or ID. This action cannot be undone.",
		Args:  cobra.ExactArgs(1),
		Example: `  # Delete a workspace (with confirmation prompt)
  loco workspace delete my-workspace

  # Delete a workspace without confirmation
  loco workspace delete my-workspace --yes

  # Delete a workspace and all its apps
  loco workspace delete my-workspace --yes --confirm-delete-apps`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			target, err := cmdutil.ResolveLogin(cmd)
			if err != nil {
				return err
			}

			workspace, err := findWorkspace(cmd, target.Clients, args[0])
			if err != nil {
				return err
			}

			confirmDeleteApps, err := cmd.Flags().GetBool("confirm-delete-apps")
			if err != nil {
				return fmt.Errorf("failed to get confirm-delete-apps flag: %w", err)
			}

			confirmed, err := cmdutil.Confirm(cmd, fmt.Sprintf(
				"Are you sure you want to delete workspace %q (ID: %s)? This cannot be undone.",
				workspace.GetName(),
				workspace.GetId(),
			))
			if err != nil || !confirmed {
				return err
			}

			delReq := &workspacev1.DeleteWorkspaceRequest{
				WorkspaceId:       workspace.GetId(),
				ConfirmDeleteApps: confirmDeleteApps,
			}

			_, err = target.Clients.Workspace.DeleteWorkspace(ctx, delReq)
			if err != nil {
				return fmt.Errorf("failed to delete workspace: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Workspace %q deleted successfully.\n", workspace.GetName())
			return nil
		},
	}

	cmdutil.AddOrgFlags(cmd)
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().Bool("confirm-delete-apps", false, "Confirm deletion of all apps in the workspace")

	return cmd
}
