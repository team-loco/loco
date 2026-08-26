package workspace

import (
	"fmt"
	"io"
	"os"
	"strconv"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/ui"
)

type deleteDeps struct {
	NewWorkspaceClient func(host string) workspacev1connect.WorkspaceServiceClient
	AskYesNo           func(prompt string) (bool, error)
	Output             io.Writer
}

func buildDeleteCmd() *cobra.Command {
	deps := deleteDeps{
		NewWorkspaceClient: func(host string) workspacev1connect.WorkspaceServiceClient {
			return workspacev1connect.NewWorkspaceServiceClient(httputil.NewHTTPClient(), host)
		},
		AskYesNo: ui.AskYesNo,
		Output:   os.Stdout,
	}
	return newDeleteCmd(deps)
}

func newDeleteCmd(deps deleteDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <workspace-id>",
		Short: "Delete a workspace",
		Long:  "Delete a workspace by ID. This action cannot be undone.",
		Args:  cobra.ExactArgs(1),
		Example: `  # Delete a workspace (with confirmation prompt)
  loco workspace delete 456

  # Delete a workspace without confirmation
  loco workspace delete 456 --yes

  # Delete a workspace and all its apps
  loco workspace delete 456 --yes --confirm-delete-apps`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			idInt, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid workspace ID: %w", err)
			}
			id := fmt.Sprintf("%d", idInt)

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

			getReq := connect.NewRequest(&workspacev1.GetWorkspaceRequest{
				WorkspaceId: id,
			})
			getReq.Header().Set("Authorization", authHeader)

			getResp, err := wsClient.GetWorkspace(ctx, getReq)
			if err != nil {
				return fmt.Errorf("failed to find workspace: %w", err)
			}

			yes, err := cmd.Flags().GetBool("yes")
			if err != nil {
				return fmt.Errorf("failed to get yes flag: %w", err)
			}
			confirmDeleteApps, err := cmd.Flags().GetBool("confirm-delete-apps")
			if err != nil {
				return fmt.Errorf("failed to get confirm-delete-apps flag: %w", err)
			}

			if !yes {
				confirm, confirmErr := deps.AskYesNo(fmt.Sprintf("Are you sure you want to delete workspace %q (ID: %s)? This cannot be undone.", getResp.Msg.Workspace.Name, id))
				if confirmErr != nil {
					return fmt.Errorf("failed to prompt for confirmation: %w", confirmErr)
				}
				if !confirm {
					fmt.Fprintln(deps.Output, "Aborted.")
					return nil
				}
			}

			delReq := connect.NewRequest(&workspacev1.DeleteWorkspaceRequest{
				WorkspaceId:       id,
				ConfirmDeleteApps: confirmDeleteApps,
			})
			delReq.Header().Set("Authorization", authHeader)

			_, err = wsClient.DeleteWorkspace(ctx, delReq)
			if err != nil {
				return fmt.Errorf("failed to delete workspace: %w", err)
			}

			fmt.Fprintf(deps.Output, "Workspace %q deleted successfully.\n", getResp.Msg.Workspace.Name)
			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().Bool("confirm-delete-apps", false, "Confirm deletion of all apps in the workspace")

	return cmd
}
