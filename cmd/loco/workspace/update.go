package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/team-loco/loco/internal/rpc"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

var errNoUpdateFlags = errors.New("at least one update flag is required (--name or --description)")

type updateDeps struct {
	UpdateWorkspace func(
		ctx context.Context,
		req *workspacev1.UpdateWorkspaceRequest,
	) (*workspacev1.UpdateWorkspaceResponse, error)
	Output io.Writer
}

func buildUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <workspace-id>",
		Short: "Update a workspace",
		Long:  "Update a workspace's properties.",
		Args:  cobra.ExactArgs(1),
		Example: `  # Rename a workspace
  loco workspace update <workspace-id> --name new-name

  # Update workspace description
  loco workspace update <workspace-id> --description "Updated description"

  # Update both name and description
  loco workspace update <workspace-id> --name new-name --description "New description"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			id := args[0]

			name, err := cmd.Flags().GetString("name")
			if err != nil {
				return fmt.Errorf("failed to get name flag: %w", err)
			}
			description, err := cmd.Flags().GetString("description")
			if err != nil {
				return fmt.Errorf("failed to get description flag: %w", err)
			}

			if name == "" && description == "" {
				return errNoUpdateFlags
			}

			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}
			locoToken, err := cmdutil.GetCurrentLocoToken(cmd)
			if err != nil {
				return err
			}

			workspaceClients := rpc.ForHost(host, locoToken.Token)
			wsClient := workspaceClients.Workspace

			deps := updateDeps{
				UpdateWorkspace: wsClient.UpdateWorkspace,
				Output:          os.Stdout,
			}

			updateReq := &workspacev1.UpdateWorkspaceRequest{
				WorkspaceId: id,
			}

			if name != "" {
				updateReq.Name = &name
			}
			if description != "" {
				updateReq.Description = &description
			}

			_, err = deps.UpdateWorkspace(ctx, updateReq)
			if err != nil {
				return fmt.Errorf("failed to update workspace: %w", err)
			}

			_, err = fmt.Fprintf(deps.Output, "Workspace updated successfully.\n")
			return err
		},
	}

	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().String("name", "", "New name for the workspace")
	cmd.Flags().String("description", "", "New description for the workspace")

	return cmd
}
