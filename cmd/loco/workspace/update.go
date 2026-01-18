package workspace

import (
	"fmt"
	"strconv"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/workspace/internal"
	workspacev1 "github.com/team-loco/loco/shared/proto/loco/workspace/v1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

type updateRunner struct {
	deps *internal.WorkspaceDeps
	id   int64
}

func buildUpdateCmd(deps *internal.WorkspaceDeps) *cobra.Command {
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
	}

	cmd.Flags().String("name", "", "New name for the workspace")
	cmd.Flags().String("description", "", "New description for the workspace")

	if deps != nil {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid workspace ID: %w", err)
			}
			runner := &updateRunner{deps: deps, id: id}
			return runner.Run(cmd, args)
		}
	}

	return cmd
}

func (r *updateRunner) Run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	// Parse ID if not already set
	if r.id == 0 {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid workspace ID: %w", err)
		}
		r.id = id
	}

	name, _ := cmd.Flags().GetString("name")
	description, _ := cmd.Flags().GetString("description")

	if name == "" && description == "" {
		return fmt.Errorf("at least one update flag is required (--name or --description)")
	}

	var paths []string
	updateReq := &workspacev1.UpdateWorkspaceRequest{
		WorkspaceId: r.id,
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
	req.Header().Set("Authorization", r.deps.AuthHeader())

	_, err := r.deps.UpdateWorkspace(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to update workspace: %w", err)
	}

	fmt.Fprintf(r.deps.Stdout, "Workspace updated successfully.\n")

	return nil
}
