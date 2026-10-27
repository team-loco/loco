package org

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

func buildInviteCmd() *cobra.Command {
	return newInviteCmd(invitationDeps{NewOrgClient: realOrgClient, Output: os.Stdout})
}

func newInviteCmd(deps invitationDeps) *cobra.Command {
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
			client, err := invitationClient(cmd, deps)
			if err != nil {
				return err
			}
			orgID, err := resolveOrgID(cmd, client)
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
			fmt.Fprintf(deps.Output, "Invited %s: %s\n", invitation.GetEmail(), statusText(invitation))
			fmt.Fprintf(deps.Output, "Link: %s\n", invitation.GetLink())
			return nil
		},
	}
	addOrgFlags(cmd)
	return cmd
}
