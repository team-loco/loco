package org

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

func buildDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an organization",
		Long:  "Delete an organization by name. This action cannot be undone.",
		Args:  cobra.ExactArgs(1),
		Example: `  # Delete an organization (with confirmation prompt)
  loco org delete my-org

  # Delete an organization without confirmation
  loco org delete my-org --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

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

			confirmed, err := cmdutil.Confirm(
				cmd, fmt.Sprintf("Are you sure you want to delete organization %q? This cannot be undone.", name),
			)
			if err != nil || !confirmed {
				return err
			}

			delReq := &orgv1.DeleteOrgRequest{
				OrgId: getResp.GetOrganization().GetId(),
			}

			_, err = orgClient.DeleteOrg(ctx, delReq)
			if err != nil {
				return fmt.Errorf("unable to delete organization %q: %w", name, err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Organization %q deleted successfully.\n", name)
			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	return cmd
}
