package org

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

const invitationTablePadding = 2

var errRepeatedInvitationPage = errors.New("invitation listing returned a repeated page")

func buildInvitationsCmd() *cobra.Command {
	return newInvitationsCmd(invitationDeps{NewOrgClient: realOrgClient, Output: os.Stdout})
}

func newInvitationsCmd(deps invitationDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invitations",
		Short: "List pending invitations",
		Long:  "List the pending invitations of an organization.",
		Args:  cobra.NoArgs,
		Example: `  # List pending invitations of the current organization
  loco org invitations`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := invitationClient(cmd, deps)
			if err != nil {
				return err
			}
			orgID, err := resolveOrgID(cmd, client)
			if err != nil {
				return err
			}

			req := &orgv1.ListInvitationsRequest{OrgId: orgID}
			var invitations []*orgv1.Invitation
			seen := make(map[string]struct{})
			for {
				resp, listErr := client.ListInvitations(cmd.Context(), req)
				if listErr != nil {
					return fmt.Errorf("unable to list invitations: %w", listErr)
				}
				invitations = append(invitations, resp.GetInvitations()...)
				next := resp.GetNextPageToken()
				if next == "" {
					break
				}
				if _, duplicate := seen[next]; duplicate {
					return errRepeatedInvitationPage
				}
				seen[next] = struct{}{}
				req.PageToken = next
			}

			if len(invitations) == 0 {
				fmt.Fprintln(deps.Output, "No pending invitations.")
				return nil
			}

			table := tabwriter.NewWriter(deps.Output, 0, 0, invitationTablePadding, ' ', 0)
			fmt.Fprintln(table, "ID\tEMAIL\tINVITED BY\tEXPIRES\tSTATUS")
			for _, invitation := range invitations {
				fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n",
					invitation.GetId(),
					invitation.GetEmail(),
					invitation.GetInvitedBy().GetEmail(),
					invitation.GetExpiresAt().AsTime().Local().Format(invitationTimeLayout),
					statusText(invitation),
				)
			}
			return table.Flush()
		},
	}
	addOrgFlags(cmd)
	return cmd
}
