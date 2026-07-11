package workspace

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/httputil"
	workspacev1 "github.com/team-loco/loco/proto/loco/workspace/v1"
	"github.com/team-loco/loco/proto/loco/workspace/v1/workspacev1connect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

type updateDeps struct {
	UpdateWorkspace func(ctx context.Context, req *connect.Request[workspacev1.UpdateWorkspaceRequest]) (*connect.Response[workspacev1.UpdateWorkspaceResponse], error)
	Output          io.Writer
}

func buildUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <workspace-id>",
		Short: "Update a workspace",
		Long:  "Update a workspace's properties.",
		Args:  cobra.ExactArgs(1),
		Example: `  # Rename a workspace
  loco workspace update 456 --name new-name

  # Update workspace description
  loco workspace update 456 --description "Updated description"

  # Update both name and description
  loco workspace update 456 --name new-name --description "New description"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			idInt, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid workspace ID: %w", err)
			}
			id := fmt.Sprintf("%d", idInt)

			name, err := cmd.Flags().GetString("name")
			if err != nil {
				return fmt.Errorf("failed to get name flag: %w", err)
			}
			description, err := cmd.Flags().GetString("description")
			if err != nil {
				return fmt.Errorf("failed to get description flag: %w", err)
			}

			if name == "" && description == "" {
				return fmt.Errorf("at least one update flag is required (--name or --description)")
			}

			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}
			locoToken, err := cmdutil.GetCurrentLocoToken()
			if err != nil {
				return err
			}

			httpClient := httputil.NewHTTPClient()
			wsClient := workspacev1connect.NewWorkspaceServiceClient(httpClient, host)

			deps := updateDeps{
				UpdateWorkspace: wsClient.UpdateWorkspace,
				Output:          os.Stdout,
			}

			var paths []string
			updateReq := &workspacev1.UpdateWorkspaceRequest{
				WorkspaceId: id,
			}

			if name != "" {
				paths = append(paths, "name")
				updateReq.Name = &name
			}
			if description != "" {
				paths = append(paths, "description")
				updateReq.Description = &description
			}

			updateReq.UpdateMask = &fieldmaskpb.FieldMask{Paths: paths}

			req := connect.NewRequest(updateReq)
			req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", locoToken.Token))

			_, err = deps.UpdateWorkspace(ctx, req)
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
