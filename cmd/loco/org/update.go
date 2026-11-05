package org

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

var errNoUpdateFlags = errors.New("at least one update flag is required (e.g., --new-name)")

func buildUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update an organization",
		Long:  "Update an organization's properties.",
		Args:  cobra.ExactArgs(1),
		Example: `  # Rename an organization
  loco org update my-org --new-name new-org-name`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			newName, err := cmd.Flags().GetString("new-name")
			if err != nil {
				return fmt.Errorf("failed to get new-name flag: %w", err)
			}
			if newName == "" {
				return errNoUpdateFlags
			}

			target, err := cmdutil.ResolveLogin(cmd)
			if err != nil {
				return err
			}

			orgClient := target.Clients.Org

			name := args[0]

			getReq := &orgv1.GetOrgRequest{
				Key: &orgv1.GetOrgRequest_OrgName{OrgName: name},
			}

			getResp, err := orgClient.GetOrg(ctx, getReq)
			if err != nil {
				return fmt.Errorf("organization %q not found: %w", name, err)
			}

			updateReq := &orgv1.UpdateOrgRequest{
				OrgId: getResp.GetOrganization().GetId(),
				Name:  &newName,
			}

			_, err = orgClient.UpdateOrg(ctx, updateReq)
			if err != nil {
				return fmt.Errorf("failed to update organization: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Organization updated successfully.\n")
			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().String("new-name", "", "New name for the organization")

	return cmd
}
