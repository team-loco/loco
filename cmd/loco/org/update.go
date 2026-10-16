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
)

var errNoUpdateFlags = errors.New("at least one update flag is required (e.g., --new-name)")

type updateDeps struct {
	NewOrgClient func(host, token string) orgv1connect.OrgServiceClient
	Output       io.Writer
}

func buildUpdateCmd() *cobra.Command {
	deps := updateDeps{
		NewOrgClient: func(host, token string) orgv1connect.OrgServiceClient {
			orgClients := rpc.ForHost(host, token)
			return orgClients.Org
		},
		Output: os.Stdout,
	}
	return newUpdateCmd(deps)
}

func newUpdateCmd(deps updateDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update an organization",
		Long:  "Update an organization's properties.",
		Args:  cobra.ExactArgs(1),
		Example: `  # Rename an organization
  loco org update my-org --new-name new-org-name`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			newName, err := cmd.Flags().GetString("new-name")
			if err != nil {
				return fmt.Errorf("failed to get new-name flag: %w", err)
			}
			if newName == "" {
				return errNoUpdateFlags
			}

			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}
			locoToken, err := cmdutil.GetCurrentLocoToken(cmd)
			if err != nil {
				return err
			}

			orgClient := deps.NewOrgClient(host, locoToken.Token)

			name := args[0]

			getReq := &orgv1.GetOrgRequest{
				Key: &orgv1.GetOrgRequest_OrgName{OrgName: name},
			}

			getResp, err := orgClient.GetOrg(ctx, getReq)
			if err != nil {
				return fmt.Errorf("organization %q not found: %w", name, err)
			}

			updateReq := &orgv1.UpdateOrgRequest{
				OrgId: getResp.GetOrganization().GetId(),
				Name:  &newName,
			}

			_, err = orgClient.UpdateOrg(ctx, updateReq)
			if err != nil {
				return fmt.Errorf("failed to update organization: %w", err)
			}

			fmt.Fprintf(deps.Output, "Organization updated successfully.\n")
			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().String("new-name", "", "New name for the organization")

	return cmd
}
