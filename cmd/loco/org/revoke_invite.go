package org

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

func buildRevokeInviteCmd() *cobra.Command {
	return newRevokeInviteCmd(invitationDeps{NewOrgClient: realOrgClient, Output: os.Stdout})
}

func newRevokeInviteCmd(deps invitationDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke-invite <id>",
		Short: "Revoke a pending invitation",
		Long:  "Revoke a pending invitation by ID. Use 'loco org invitations' to find the ID.",
		Args:  cobra.ExactArgs(1),
		Example: `  # Revoke an invitation
  loco org revoke-invite 0198c2d4-1111-7000-8000-000000000001`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := invitationClient(cmd, deps)
			if err != nil {
				return err
			}
			request := &orgv1.RevokeInvitationRequest{Id: args[0]}
			if _, err = client.RevokeInvitation(cmd.Context(), request); err != nil {
				return fmt.Errorf("unable to revoke invitation %s: %w", args[0], err)
			}
			fmt.Fprintf(deps.Output, "Invitation %s revoked.\n", args[0])
			return nil
		},
	}
	cmd.Flags().String("host", "", "API host URL")
	return cmd
}
