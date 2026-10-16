package workspace

import (
	"github.com/spf13/cobra"
)

// BuildWorkspaceCmd creates the "workspace" parent command with all subcommands.
func BuildWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workspace",
		Aliases: []string{"ws"},
		Short:   "Manage workspaces",
		Long:    "Commands for creating, listing, and managing workspaces.",
		Example: `  # List all workspaces
  loco workspace list

  # List workspaces in a specific organization
  loco workspace list --org-id <organization-id>

  # Create a new workspace
  loco workspace create my-workspace --org-id <organization-id>

  # Update a workspace
  loco workspace update <workspace-id> --name new-name

  # Delete a workspace
  loco workspace delete <workspace-id>`,
	}

	cmd.AddCommand(
		buildListCmd(),
		buildCreateCmd(),
		buildDeleteCmd(),
		buildUpdateCmd(),
	)

	return cmd
}
