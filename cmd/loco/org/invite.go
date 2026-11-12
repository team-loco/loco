package org

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

func buildInviteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invite <email>",
		Short: "Invite someone to an organization",
		Long:  "Invite an email address to join an organization as a member. The invitation is claimed when they sign in.",
		Args:  cobra.ExactArgs(1),
		Example: `  # Invite a teammate to the current organization
  loco org invite dev@example.com

  # Invite to a specific organization
  loco org invite dev@example.com --org my-org`,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := cmdutil.ResolveLogin(cmd)
			if err != nil {
				return err
			}
			client := target.Clients.Org
			orgID, err := resolveOrgID(cmd, target.Clients)
			if err != nil {
				return err
			}
			resp, err := client.InviteOrgMember(cmd.Context(), &orgv1.InviteOrgMemberRequest{
				OrgId: orgID,
				Email: args[0],
			})
			if err != nil {
				return fmt.Errorf("unable to invite %s: %w", args[0], err)
			}
			invitation := resp.GetInvitation()
			fmt.Fprintf(cmd.OutOrStdout(), "Invited %s\n", invitation.GetEmail())
			fmt.Fprintf(cmd.OutOrStdout(), "Link: %s\n", invitation.GetLink())
			return nil
		},
	}
	addOrgFlags(cmd)
	return cmd
}
