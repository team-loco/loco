package token

import (
	"github.com/spf13/cobra"
)

// BuildTokenCmd creates the "token" parent command with all subcommands.
func BuildTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage API tokens",
		Long: `Commands for creating, listing, and managing access tokens.

Tokens can be scoped to different entity types:
  - user: Personal tokens for your account
  - org: Organization-level tokens
  - workspace: Workspace-scoped tokens
  - resource: Resource-specific tokens`,
		Example: `  # Show your current session token
  loco token show

  # Create a personal API token
  loco token create my-ci-token --scope read

  # Create an organization-scoped token
  loco token create deploy-token --entity-type org --entity-id 123 --scope write

  # List all your personal tokens
  loco token list

  # List tokens for an organization
  loco token list --entity-type org --entity-id 123

  # Delete a token
  loco token delete my-ci-token`,
	}

	cmd.AddCommand(
		buildShowCmd(),
		buildRevokeCmd(),
		buildCreateCmd(),
		buildListCmd(),
		buildDeleteCmd(),
	)

	return cmd
}
