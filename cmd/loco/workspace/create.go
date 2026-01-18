package workspace

import (
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/workspace/internal"
	workspacev1 "github.com/team-loco/loco/shared/proto/workspace/v1"
)

type createRunner struct {
	deps *internal.WorkspaceDeps
	name string
}

func buildCreateCmd(deps *internal.WorkspaceDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a workspace",
		Long:  "Create a new workspace within an organization.",
		Args:  cobra.ExactArgs(1),
		Example: `  # Create a workspace in an organization
  loco workspace create my-workspace --org-id 123

  # Create a workspace with a description
  loco workspace create my-workspace --org-id 123 --description "Production environment"`,
	}

	cmd.Flags().Int64("org-id", 0, "Organization ID (required)")
	cmd.Flags().String("description", "", "Workspace description")
	cmd.MarkFlagRequired("org-id")

	if deps != nil {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			runner := &createRunner{deps: deps, name: args[0]}
			return runner.Run(cmd)
		}
	}

	return cmd
}

func (r *createRunner) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	orgID, _ := cmd.Flags().GetInt64("org-id")
	description, _ := cmd.Flags().GetString("description")

	createReq := &workspacev1.CreateWorkspaceRequest{
		OrgId: orgID,
		Name:  r.name,
	}
	if description != "" {
		createReq.Description = &description
	}

	req := connect.NewRequest(createReq)
	req.Header().Set("Authorization", r.deps.AuthHeader())

	resp, err := r.deps.CreateWorkspace(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to create workspace: %w", err)
	}

	fmt.Fprintf(r.deps.Stdout, "Workspace %q created successfully (ID: %d)\n", r.name, resp.Msg.WorkspaceId)

	return nil
}
