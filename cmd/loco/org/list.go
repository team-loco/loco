package org

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
)

func buildListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List organizations",
		Long:  "List all organizations you have access to.",
		Args:  cobra.NoArgs,
		Example: `  # List all your organizations
  loco org list`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			target, err := cmdutil.ResolveLogin(cmd)
			if err != nil {
				return err
			}

			organizations, err := cmdutil.ListUserOrgs(ctx, target.Clients.User, target.Clients.Org)
			if err != nil {
				return fmt.Errorf("unable to list organizations: %w", err)
			}

			if len(organizations) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No organizations found.")
				return nil
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Organizations:")
			for _, org := range organizations {
				fmt.Fprintf(cmd.OutOrStdout(), "  - %s (ID: %s)\n", org.GetName(), org.GetId())
			}

			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")

	return cmd
}
