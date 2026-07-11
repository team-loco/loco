package org

import (
	"fmt"
	"io"
	"os"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/httputil"
	orgv1 "github.com/team-loco/loco/proto/loco/org/v1"
	"github.com/team-loco/loco/proto/loco/org/v1/orgv1connect"
)

type createDeps struct {
	NewOrgClient func(host string) orgv1connect.OrgServiceClient
	Output       io.Writer
}

func buildCreateCmd() *cobra.Command {
	deps := createDeps{
		NewOrgClient: func(host string) orgv1connect.OrgServiceClient {
			return orgv1connect.NewOrgServiceClient(httputil.NewHTTPClient(), host)
		},
		Output: os.Stdout,
	}
	return newCreateCmd(deps)
}

func newCreateCmd(deps createDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create an organization",
		Long:  "Create a new organization. If name is not provided, a default name will be generated.",
		Args:  cobra.MaximumNArgs(1),
		Example: `  # Create an organization with a specific name
  loco org create my-company

  # Create an organization with an auto-generated name
  loco org create`,
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
			authHeader := fmt.Sprintf("Bearer %s", locoToken.Token)

			var name string
			if len(args) > 0 {
				name = args[0]
			}

			createReq := &orgv1.CreateOrgRequest{}
			if name != "" {
				createReq.Name = &name
			}

			req := connect.NewRequest(createReq)
			req.Header().Set("Authorization", authHeader)

			resp, err := orgClient.CreateOrg(ctx, req)
			if err != nil {
				if name != "" {
					return fmt.Errorf("unable to create organization %q: %w", name, err)
				}
				return fmt.Errorf("unable to create organization: %w", err)
			}

			fmt.Fprintf(deps.Output, "Organization %q created successfully (ID: %s)\n", name, resp.Msg.OrgId)
			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")

	return cmd
}
