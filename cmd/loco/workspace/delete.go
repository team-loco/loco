package workspace

import (
	"fmt"
	"strconv"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/workspace/internal"
	"github.com/team-loco/loco/internal/ui"
	workspacev1 "github.com/team-loco/loco/shared/proto/workspace/v1"
)

type deleteRunner struct {
	deps *internal.WorkspaceDeps
	id   int64
}

func buildDeleteCmd(deps *internal.WorkspaceDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <workspace-id>",
		Short: "Delete a workspace",
		Long:  "Delete a workspace by ID. This action cannot be undone.",
		Args:  cobra.ExactArgs(1),
		Example: `  # Delete a workspace (with confirmation prompt)
  loco workspace delete 456

  # Delete a workspace without confirmation
  loco workspace delete 456 --force

  # Delete a workspace and all its apps
  loco workspace delete 456 --force --confirm-delete-apps`,
	}

	cmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")
	cmd.Flags().Bool("confirm-delete-apps", false, "Confirm deletion of all apps in the workspace")

	if deps != nil {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid workspace ID: %w", err)
			}
			runner := &deleteRunner{deps: deps, id: id}
			return runner.Run(cmd, args)
		}
	}

	return cmd
}

func (r *deleteRunner) Run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	// Parse ID if not already set
	if r.id == 0 {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid workspace ID: %w", err)
		}
		r.id = id
	}

	// Get workspace info first
	getReq := connect.NewRequest(&workspacev1.GetWorkspaceRequest{
		WorkspaceId: r.id,
	})
	getReq.Header().Set("Authorization", r.deps.AuthHeader())

	getResp, err := r.deps.GetWorkspace(ctx, getReq)
	if err != nil {
		return fmt.Errorf("failed to find workspace: %w", err)
	}

	force, _ := cmd.Flags().GetBool("force")
	confirmDeleteApps, _ := cmd.Flags().GetBool("confirm-delete-apps")

	if !force {
		confirm, err := ui.AskYesNo(fmt.Sprintf("Are you sure you want to delete workspace %q (ID: %d)? This cannot be undone.", getResp.Msg.Workspace.Name, r.id))
		if err != nil {
			return fmt.Errorf("failed to prompt for confirmation: %w", err)
		}
		if !confirm {
			fmt.Fprintln(r.deps.Stdout, "Aborted.")
			return nil
		}
	}

	delReq := connect.NewRequest(&workspacev1.DeleteWorkspaceRequest{
		WorkspaceId:       r.id,
		ConfirmDeleteApps: confirmDeleteApps,
	})
	delReq.Header().Set("Authorization", r.deps.AuthHeader())

	_, err = r.deps.DeleteWorkspace(ctx, delReq)
	if err != nil {
		return fmt.Errorf("failed to delete workspace: %w", err)
	}

	fmt.Fprintf(r.deps.Stdout, "Workspace %q deleted successfully.\n", getResp.Msg.Workspace.Name)

	return nil
}
