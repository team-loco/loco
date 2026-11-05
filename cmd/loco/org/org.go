package org

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/internal/rpc"
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
  loco org delete my-org

  # Invite someone to the current organization
  loco org invite dev@example.com`,
	}

	cmd.AddCommand(
		buildListCmd(),
		buildCreateCmd(),
		buildDeleteCmd(),
		buildUpdateCmd(),
		buildInviteCmd(),
		buildInvitationsCmd(),
		buildRevokeInviteCmd(),
	)

	return cmd
}

func findOrg(ctx context.Context, clients *rpc.Clients, ref string) (*orgv1.Organization, error) {
	orgs, err := cmdutil.ListUserOrgs(ctx, clients.User, clients.Org)
	if err != nil {
		return nil, fmt.Errorf("unable to list organizations: %w", err)
	}
	return cmdutil.FindOrg(orgs, ref)
}
