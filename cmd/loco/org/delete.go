package org

import (
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/org/internal"
	"github.com/team-loco/loco/internal/ui"
	orgv1 "github.com/team-loco/loco/shared/proto/loco/org/v1"
)

type deleteRunner struct {
	deps *internal.OrgDeps
	name string
}

func buildDeleteCmd(deps *internal.OrgDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an organization",
		Long:  "Delete an organization by name. This action cannot be undone.",
		Args:  cobra.ExactArgs(1),
		Example: `  # Delete an organization (with confirmation prompt)
  loco org delete my-org

  # Delete an organization without confirmation
  loco org delete my-org --force`,
	}

	cmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")

	if deps != nil {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			runner := &deleteRunner{deps: deps, name: args[0]}
			return runner.Run(cmd)
		}
	}

	return cmd
}

func (r *deleteRunner) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	// First, get the org by name to get its ID
	getReq := connect.NewRequest(&orgv1.GetOrgRequest{
		Key: &orgv1.GetOrgRequest_OrgName{OrgName: r.name},
	})
	getReq.Header().Set("Authorization", r.deps.AuthHeader())

	getResp, err := r.deps.GetOrg(ctx, getReq)
	if err != nil {
		return fmt.Errorf("failed to find organization %q: %w", r.name, err)
	}

	force, _ := cmd.Flags().GetBool("force")
	if !force {
		confirm, err := ui.AskYesNo(fmt.Sprintf("Are you sure you want to delete organization %q? This cannot be undone.", r.name))
		if err != nil {
			return fmt.Errorf("failed to prompt for confirmation: %w", err)
		}
		if !confirm {
			fmt.Fprintln(r.deps.Stdout, "Aborted.")
			return nil
		}
	}

	delReq := connect.NewRequest(&orgv1.DeleteOrgRequest{
		OrgId: getResp.Msg.Organization.Id,
	})
	delReq.Header().Set("Authorization", r.deps.AuthHeader())

	_, err = r.deps.DeleteOrg(ctx, delReq)
	if err != nil {
		return fmt.Errorf("failed to delete organization: %w", err)
	}

	fmt.Fprintf(r.deps.Stdout, "Organization %q deleted successfully.\n", r.name)

	return nil
}
