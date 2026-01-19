package org

import (
	"fmt"
	"io"
	"os"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/shared"
	orgv1 "github.com/team-loco/loco/shared/proto/loco/org/v1"
	"github.com/team-loco/loco/shared/proto/loco/org/v1/orgv1connect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

type updateDeps struct {
	NewOrgClient func(host string) orgv1connect.OrgServiceClient
	Output       io.Writer
}

func buildUpdateCmd() *cobra.Command {
	deps := updateDeps{
		NewOrgClient: func(host string) orgv1connect.OrgServiceClient {
			return orgv1connect.NewOrgServiceClient(shared.NewHTTPClient(), host)
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

			newName, _ := cmd.Flags().GetString("new-name")
			if newName == "" {
				return fmt.Errorf("at least one update flag is required (e.g., --new-name)")
			}

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

			name := args[0]

			getReq := connect.NewRequest(&orgv1.GetOrgRequest{
				Key: &orgv1.GetOrgRequest_OrgName{OrgName: name},
			})
			getReq.Header().Set("Authorization", authHeader)

			getResp, err := orgClient.GetOrg(ctx, getReq)
			if err != nil {
				return fmt.Errorf("organization %q not found: %w", name, err)
			}

			updateReq := connect.NewRequest(&orgv1.UpdateOrgRequest{
				OrgId:      getResp.Msg.Organization.Id,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
				Name:       &newName,
			})
			updateReq.Header().Set("Authorization", authHeader)

			_, err = orgClient.UpdateOrg(ctx, updateReq)
			if err != nil {
				return fmt.Errorf("failed to update organization: %w", err)
			}

			fmt.Fprintf(deps.Output, "Organization updated successfully.\n")
			return nil
		},
	}

	cmd.Flags().String("new-name", "", "New name for the organization")

	return cmd
}
