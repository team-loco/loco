package workspace

import (
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/workspace/internal"
	userv1 "github.com/team-loco/loco/shared/proto/user/v1"
	workspacev1 "github.com/team-loco/loco/shared/proto/workspace/v1"
)

type listRunner struct {
	deps *internal.WorkspaceDeps
}

func buildListCmd(deps *internal.WorkspaceDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List workspaces",
		Long:  "List all workspaces you have access to.",
		Args:  cobra.NoArgs,
		Example: `  # List all your workspaces
  loco workspace list

  # List workspaces in a specific organization
  loco workspace list --org-id 123`,
	}

	cmd.Flags().Int64("org-id", 0, "Filter by organization ID")

	if deps != nil {
		runner := &listRunner{deps: deps}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			return runner.Run(cmd)
		}
	}

	return cmd
}

func (r *listRunner) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	orgID, _ := cmd.Flags().GetInt64("org-id")

	if orgID != 0 {
		// List workspaces for a specific org
		req := connect.NewRequest(&workspacev1.ListOrgWorkspacesRequest{
			OrgId: orgID,
		})
		req.Header().Set("Authorization", r.deps.AuthHeader())

		resp, err := r.deps.ListOrgWorkspaces(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to list workspaces: %w", err)
		}

		if len(resp.Msg.Workspaces) == 0 {
			fmt.Fprintln(r.deps.Stdout, "No workspaces found in this organization.")
			return nil
		}

		fmt.Fprintln(r.deps.Stdout, "Workspaces:")
		for _, ws := range resp.Msg.Workspaces {
			fmt.Fprintf(r.deps.Stdout, "  - %s (ID: %d)\n", ws.Name, ws.Id)
		}
	} else {
		// Get current user ID via WhoAmI
		whoAmIReq := connect.NewRequest(&userv1.WhoAmIRequest{})
		whoAmIReq.Header().Set("Authorization", r.deps.AuthHeader())

		whoAmIResp, err := r.deps.WhoAmI(ctx, whoAmIReq)
		if err != nil {
			return fmt.Errorf("failed to get current user: %w", err)
		}

		req := connect.NewRequest(&workspacev1.ListUserWorkspacesRequest{
			UserId: whoAmIResp.Msg.User.Id,
		})
		req.Header().Set("Authorization", r.deps.AuthHeader())

		resp, err := r.deps.ListUserWorkspaces(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to list workspaces: %w", err)
		}

		if len(resp.Msg.Workspaces) == 0 {
			fmt.Fprintln(r.deps.Stdout, "No workspaces found.")
			return nil
		}

		fmt.Fprintln(r.deps.Stdout, "Workspaces:")
		for _, ws := range resp.Msg.Workspaces {
			fmt.Fprintf(r.deps.Stdout, "  - %s (ID: %d, Org: %d)\n", ws.Name, ws.Id, ws.OrgId)
		}
	}

	return nil
}
