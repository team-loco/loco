package workspace

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func buildCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a workspace",
		Long:  "Create a new workspace within an organization.",
		Args:  cobra.ExactArgs(1),
		Example: `  # Create a workspace in the current organization
  loco workspace create my-workspace

  # Create a workspace in a specific organization, with a description
  loco workspace create my-workspace --org my-org --description "Production environment"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			target, err := cmdutil.ResolveLogin(cmd)
			if err != nil {
				return err
			}

			name := args[0]
			orgID, err := cmdutil.ResolveOrgID(cmd, target.Clients)
			if err != nil {
				return err
			}
			description, err := cmd.Flags().GetString("description")
			if err != nil {
				return fmt.Errorf("failed to get description flag: %w", err)
			}

			createReq := &workspacev1.CreateWorkspaceRequest{
				OrgId: orgID,
				Name:  name,
			}
			if description != "" {
				createReq.Description = &description
			}

			resp, err := target.Clients.Workspace.CreateWorkspace(ctx, createReq)
			if err != nil {
				return fmt.Errorf("failed to create workspace: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Workspace %q created successfully (ID: %s)\n", name, resp.GetWorkspaceId())
			return nil
		},
	}

	cmdutil.AddOrgFlags(cmd)
	cmd.Flags().String("description", "", "Workspace description")

	return cmd
}
