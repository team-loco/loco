package workspace

import (
	"fmt"
	"io"
	"os"

	"github.com/team-loco/loco/internal/rpc"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
)

type createDeps struct {
	NewWorkspaceClient func(host, token string) workspacev1connect.WorkspaceServiceClient
	Output             io.Writer
}

func buildCreateCmd() *cobra.Command {
	deps := createDeps{
		NewWorkspaceClient: func(host, token string) workspacev1connect.WorkspaceServiceClient {
			workspaceClients := rpc.ForHost(host, token)
			return workspaceClients.Workspace
		},
		Output: os.Stdout,
	}
	return newCreateCmd(deps)
}

func newCreateCmd(deps createDeps) *cobra.Command {
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

			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}
			locoToken, err := cmdutil.GetCurrentLocoToken(cmd)
			if err != nil {
				return err
			}

			wsClient := deps.NewWorkspaceClient(host, locoToken.Token)

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

			resp, err := wsClient.CreateWorkspace(ctx, createReq)
			if err != nil {
				return fmt.Errorf("failed to create workspace: %w", err)
			}

			fmt.Fprintf(deps.Output, "Workspace %q created successfully (ID: %s)\n", name, resp.GetWorkspaceId())
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
