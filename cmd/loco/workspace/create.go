package workspace

import (
	"fmt"
	"io"
	"os"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
	"github.com/team-loco/loco/internal/httputil"
)

type createDeps struct {
	NewWorkspaceClient func(host string) workspacev1connect.WorkspaceServiceClient
	Output             io.Writer
}

func buildCreateCmd() *cobra.Command {
	deps := createDeps{
		NewWorkspaceClient: func(host string) workspacev1connect.WorkspaceServiceClient {
			return workspacev1connect.NewWorkspaceServiceClient(httputil.NewHTTPClient(), host)
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
  loco workspace create my-workspace --org-id 123

  # Create a workspace with a description
  loco workspace create my-workspace --org-id 123 --description "Production environment"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}
			locoToken, err := cmdutil.GetCurrentLocoToken()
			if err != nil {
				return err
			}

			wsClient := deps.NewWorkspaceClient(host)
			authHeader := fmt.Sprintf("Bearer %s", locoToken.Token)

			name := args[0]
			orgIDInt, err := cmd.Flags().GetInt64("org-id")
			if err != nil {
				return fmt.Errorf("failed to get org-id flag: %w", err)
			}
			description, err := cmd.Flags().GetString("description")
			if err != nil {
				return fmt.Errorf("failed to get description flag: %w", err)
			}

			createReq := &workspacev1.CreateWorkspaceRequest{
				OrgId: fmt.Sprintf("%d", orgIDInt),
				Name:  name,
			}
			if description != "" {
				createReq.Description = &description
			}

			req := connect.NewRequest(createReq)
			req.Header().Set("Authorization", authHeader)

			resp, err := wsClient.CreateWorkspace(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to create workspace: %w", err)
			}

			fmt.Fprintf(deps.Output, "Workspace %q created successfully (ID: %s)\n", name, resp.Msg.WorkspaceId)
			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().Int64("org-id", 0, "Organization ID (required)")
	cmd.Flags().String("description", "", "Workspace description")
	if err := cmd.MarkFlagRequired("org-id"); err != nil {
		panic(fmt.Sprintf("failed to mark org-id as required: %v", err))
	}

	return cmd
}
