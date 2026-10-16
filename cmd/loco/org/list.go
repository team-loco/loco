package org

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/team-loco/loco/internal/rpc"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
)

var errRepeatedOrganizationPage = errors.New("organization listing returned a repeated page")

type listDeps struct {
	NewOrgClient  func(host, token string) orgv1connect.OrgServiceClient
	NewUserClient func(host, token string) userv1connect.UserServiceClient
	Output        io.Writer
}

func buildListCmd() *cobra.Command {
	deps := listDeps{
		NewOrgClient: func(host, token string) orgv1connect.OrgServiceClient {
			orgClients := rpc.ForHost(host, token)
			return orgClients.Org
		},
		NewUserClient: func(host, token string) userv1connect.UserServiceClient {
			userClients := rpc.ForHost(host, token)
			return userClients.User
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

			orgClient := deps.NewOrgClient(host, locoToken.Token)
			userClient := deps.NewUserClient(host, locoToken.Token)

			whoAmIReq := &userv1.WhoAmIRequest{}

			whoAmIResp, err := userClient.WhoAmI(ctx, whoAmIReq)
			if err != nil {
				return fmt.Errorf("unable to get current user: %w", err)
			}

			req := &orgv1.ListUserOrgsRequest{
				UserId: whoAmIResp.GetUser().GetId(),
			}

			var organizations []*orgv1.Organization
			seen := make(map[string]struct{})
			for {
				resp, err := orgClient.ListUserOrgs(ctx, req)
				if err != nil {
					return fmt.Errorf("unable to list organizations: %w", err)
				}
				organizations = append(organizations, resp.GetOrgs()...)
				next := resp.GetNextPageToken()
				if next == "" {
					break
				}
				if _, duplicate := seen[next]; duplicate {
					return errRepeatedOrganizationPage
				}
				seen[next] = struct{}{}
				req.PageToken = next
			}

			if len(organizations) == 0 {
				fmt.Fprintln(deps.Output, "No organizations found.")
				return nil
			}

			fmt.Fprintln(deps.Output, "Organizations:")
			for _, org := range organizations {
				fmt.Fprintf(deps.Output, "  - %s (ID: %s)\n", org.GetName(), org.GetId())
			}

			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")

	return cmd
}
