package org

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

const invitationTablePadding = 2

func buildInvitationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invitations",
		Short: "List pending invitations",
		Long:  "List the pending invitations of an organization.",
		Args:  cobra.NoArgs,
		Example: `  # List pending invitations of the current organization
  loco org invitations`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := cmdutil.ResolveLogin(cmd)
			if err != nil {
				return err
			}
			client := target.Clients.Org
			orgID, err := resolveOrgID(cmd, target.Clients)
			if err != nil {
				return err
			}

			invitations, err := cmdutil.Paginate(func(token string) ([]*orgv1.Invitation, string, error) {
				resp, listErr := client.ListInvitations(
					cmd.Context(),
					&orgv1.ListInvitationsRequest{OrgId: orgID, PageToken: token},
				)
				if listErr != nil {
					return nil, "", listErr
				}
				return resp.GetInvitations(), resp.GetNextPageToken(), nil
			})
			if err != nil {
				return fmt.Errorf("unable to list invitations: %w", err)
			}

			if len(invitations) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No pending invitations.")
				return nil
			}

			table := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, invitationTablePadding, ' ', 0)
			fmt.Fprintln(table, "ID\tEMAIL\tINVITED BY\tEXPIRES")
			for _, invitation := range invitations {
				fmt.Fprintf(table, "%s\t%s\t%s\t%s\n",
					invitation.GetId(),
					invitation.GetEmail(),
					invitation.GetInvitedBy().GetEmail(),
					invitation.GetExpiresAt().AsTime().Local().Format(invitationTimeLayout),
				)
			}
			return table.Flush()
		},
	}
	addOrgFlags(cmd)
	return cmd
}
