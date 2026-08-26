package org

import (
	"fmt"
	"io"
	"os"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/internal/httputil"
)

type listDeps struct {
	NewOrgClient  func(host string) orgv1connect.OrgServiceClient
	NewUserClient func(host string) userv1connect.UserServiceClient
	Output        io.Writer
}

func buildListCmd() *cobra.Command {
	deps := listDeps{
		NewOrgClient: func(host string) orgv1connect.OrgServiceClient {
			return orgv1connect.NewOrgServiceClient(httputil.NewHTTPClient(), host)
		},
		NewUserClient: func(host string) userv1connect.UserServiceClient {
			return userv1connect.NewUserServiceClient(httputil.NewHTTPClient(), host)
		},
		Output: os.Stdout,
	}
	return newListCmd(deps)
}

func newListCmd(deps listDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List organizations",
		Long:  "List all organizations you have access to.",
		Args:  cobra.NoArgs,
		Example: `  # List all your organizations
  loco org list`,
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

			orgClient := deps.NewOrgClient(host)
			userClient := deps.NewUserClient(host)
			authHeader := fmt.Sprintf("Bearer %s", locoToken.Token)

			whoAmIReq := connect.NewRequest(&userv1.WhoAmIRequest{})
			whoAmIReq.Header().Set("Authorization", authHeader)

			whoAmIResp, err := userClient.WhoAmI(ctx, whoAmIReq)
			if err != nil {
				return fmt.Errorf("unable to get current user: %w", err)
			}

			req := connect.NewRequest(&orgv1.ListUserOrgsRequest{
				UserId: whoAmIResp.Msg.User.Id,
			})
			req.Header().Set("Authorization", authHeader)

			resp, err := orgClient.ListUserOrgs(ctx, req)
			if err != nil {
				return fmt.Errorf("unable to list organizations: %w", err)
			}

			if len(resp.Msg.Orgs) == 0 {
				fmt.Fprintln(deps.Output, "No organizations found.")
				return nil
			}

			fmt.Fprintln(deps.Output, "Organizations:")
			for _, org := range resp.Msg.Orgs {
				fmt.Fprintf(deps.Output, "  - %s (ID: %s)\n", org.Name, org.Id)
			}

			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")

	return cmd
}
