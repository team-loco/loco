package org

import (
	"github.com/spf13/cobra"
)

// BuildOrgCmd creates the "org" parent command with all subcommands.
func BuildOrgCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "org",
		Short: "Manage organizations",
		Long:  "Commands for creating, listing, and managing organizations.",
		Example: `  # List all organizations
  loco org list

  # Create a new organization
  loco org create my-org

  # Rename an organization
  loco org update my-org --new-name new-org

  # Delete an organization
  loco org delete my-org`,
	}

	cmd.AddCommand(
		buildListCmd(),
		buildCreateCmd(),
		buildDeleteCmd(),
		buildUpdateCmd(),
	)

	return cmd
}
