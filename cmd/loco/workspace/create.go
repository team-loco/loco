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
		Example: `  # Create a workspace in an organization
  loco workspace create my-workspace --org-id <organization-id>

  # Create a workspace with a description
  loco workspace create my-workspace --org-id <organization-id> --description "Production environment"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			target, err := cmdutil.ResolveLogin(cmd)
			if err != nil {
				return err
			}

			name := args[0]
			orgID, err := cmd.Flags().GetString("org-id")
			if err != nil {
				return fmt.Errorf("failed to get org-id flag: %w", err)
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

	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().String("org-id", "", "Organization ID (required)")
	cmd.Flags().String("description", "", "Workspace description")
	if err := cmd.MarkFlagRequired("org-id"); err != nil {
		panic(fmt.Sprintf("failed to mark org-id as required: %v", err))
	}

	return cmd
}
