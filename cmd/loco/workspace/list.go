package workspace

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/team-loco/loco/internal/rpc"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

type listDeps struct {
	WhoAmI func(
		ctx context.Context,
		req *connect.Request[userv1.WhoAmIRequest],
	) (*connect.Response[userv1.WhoAmIResponse], error)
	ListUserWorkspaces func(
		ctx context.Context,
		req *connect.Request[workspacev1.ListUserWorkspacesRequest],
	) (*connect.Response[workspacev1.ListUserWorkspacesResponse], error)
	ListOrgWorkspaces func(
		ctx context.Context,
		req *connect.Request[workspacev1.ListOrgWorkspacesRequest],
	) (*connect.Response[workspacev1.ListOrgWorkspacesResponse], error)
	Output io.Writer
}

func buildListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List workspaces",
		Long:  "List all workspaces you have access to.",
		Args:  cobra.NoArgs,
		Example: `  # List all your workspaces
  loco workspace list

  # List workspaces in a specific organization
  loco workspace list --org-id 123`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}
			locoToken, err := cmdutil.GetCurrentLocoToken(cmd)
			if err != nil {
				return err
			}

			clients := rpc.ForHost(host, locoToken.Token)
			wsClient := clients.Workspace
			userClient := clients.User

			deps := listDeps{
				WhoAmI:             userClient.WhoAmI,
				ListUserWorkspaces: wsClient.ListUserWorkspaces,
				ListOrgWorkspaces:  wsClient.ListOrgWorkspaces,
				Output:             os.Stdout,
			}

			orgIDInt, err := cmd.Flags().GetInt64("org-id")
			if err != nil {
				return fmt.Errorf("failed to get org-id flag: %w", err)
			}

			if orgIDInt != 0 {
				req := connect.NewRequest(&workspacev1.ListOrgWorkspacesRequest{
					OrgId: strconv.FormatInt(orgIDInt, 10),
				})

				resp, err := deps.ListOrgWorkspaces(ctx, req)
				if err != nil {
					return fmt.Errorf("failed to list workspaces: %w", err)
				}

				if len(resp.Msg.GetWorkspaces()) == 0 {
					_, err = fmt.Fprintln(deps.Output, "No workspaces found in this organization.")
					return err
				}

				_, err = fmt.Fprintln(deps.Output, "Workspaces:")
				if err != nil {
					return err
				}
				for _, ws := range resp.Msg.GetWorkspaces() {
					_, err = fmt.Fprintf(deps.Output, "  - %s (ID: %s)\n", ws.GetName(), ws.GetId())
					if err != nil {
						return err
					}
				}
			} else {
				whoAmIReq := connect.NewRequest(&userv1.WhoAmIRequest{})

				whoAmIResp, err := deps.WhoAmI(ctx, whoAmIReq)
				if err != nil {
					return fmt.Errorf("failed to get current user: %w", err)
				}

				req := connect.NewRequest(&workspacev1.ListUserWorkspacesRequest{
					UserId: whoAmIResp.Msg.GetUser().GetId(),
				})

				resp, err := deps.ListUserWorkspaces(ctx, req)
				if err != nil {
					return fmt.Errorf("failed to list workspaces: %w", err)
				}

				if len(resp.Msg.GetWorkspaces()) == 0 {
					_, err = fmt.Fprintln(deps.Output, "No workspaces found.")
					return err
				}

				_, err = fmt.Fprintln(deps.Output, "Workspaces:")
				if err != nil {
					return err
				}
				for _, ws := range resp.Msg.GetWorkspaces() {
					_, err = fmt.Fprintf(deps.Output, "  - %s (ID: %s)\n", ws.GetName(), ws.GetId())
					if err != nil {
						return err
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().Int64("org-id", 0, "Filter by organization ID")

	return cmd
}
