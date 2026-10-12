package org

import (
	"fmt"
	"io"
	"os"

	"github.com/team-loco/loco/internal/rpc"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	"github.com/team-loco/loco/internal/ui"
)

type deleteDeps struct {
	NewOrgClient func(host, token string) orgv1connect.OrgServiceClient
	AskYesNo     func(prompt string) (bool, error)
	Output       io.Writer
}

func buildDeleteCmd() *cobra.Command {
	deps := deleteDeps{
		NewOrgClient: func(host, token string) orgv1connect.OrgServiceClient {
			orgClients := rpc.ForHost(host, token)
			return orgClients.Org
		},
		AskYesNo: ui.AskYesNo,
		Output:   os.Stdout,
	}
	return newDeleteCmd(deps)
}

func newDeleteCmd(deps deleteDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an organization",
		Long:  "Delete an organization by name. This action cannot be undone.",
		Args:  cobra.ExactArgs(1),
		Example: `  # Delete an organization (with confirmation prompt)
  loco org delete my-org

  # Delete an organization without confirmation
  loco org delete my-org --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			name := args[0]
			yes, err := cmd.Flags().GetBool("yes")
			if err != nil {
				return fmt.Errorf("failed to get yes flag: %w", err)
			}

			getReq := &orgv1.GetOrgRequest{
				Key: &orgv1.GetOrgRequest_OrgName{OrgName: name},
			}

			getResp, err := orgClient.GetOrg(ctx, getReq)
			if err != nil {
				return fmt.Errorf("organization %q not found: %w", name, err)
			}

			if !yes {
				confirm, confirmErr := deps.AskYesNo(
					fmt.Sprintf("Are you sure you want to delete organization %q? This cannot be undone.", name),
				)
				if confirmErr != nil {
					return fmt.Errorf("unable to prompt for confirmation: %w", confirmErr)
				}
				if !confirm {
					fmt.Fprintln(deps.Output, "Aborted.")
					return nil
				}
			}

			delReq := &orgv1.DeleteOrgRequest{
				OrgId: getResp.GetOrganization().GetId(),
			}

			_, err = orgClient.DeleteOrg(ctx, delReq)
			if err != nil {
				return fmt.Errorf("unable to delete organization %q: %w", name, err)
			}

			fmt.Fprintf(deps.Output, "Organization %q deleted successfully.\n", name)
			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	return cmd
}
