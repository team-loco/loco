package org

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

func buildCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create an organization",
		Long:  "Create a new organization. If name is not provided, a default name will be generated.",
		Args:  cobra.MaximumNArgs(1),
		Example: `  # Create an organization with a specific name
  loco org create my-company

  # Create an organization with an auto-generated name
  loco org create`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			target, err := cmdutil.ResolveLogin(cmd)
			if err != nil {
				return err
			}

			var name string
			if len(args) > 0 {
				name = args[0]
			}

			createReq := &orgv1.CreateOrgRequest{}
			if name != "" {
				createReq.Name = &name
			}

			req := createReq

			resp, err := target.Clients.Org.CreateOrg(ctx, req)
			if err != nil {
				if name != "" {
					return fmt.Errorf("unable to create organization %q: %w", name, err)
				}
				return fmt.Errorf("unable to create organization: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Organization %q created successfully (ID: %s)\n", name, resp.GetOrgId())
			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")

	return cmd
}
