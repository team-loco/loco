package org

import (
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/org/internal"
	orgv1 "github.com/team-loco/loco/shared/proto/loco/org/v1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

type updateRunner struct {
	deps *internal.OrgDeps
	name string
}

func buildUpdateCmd(deps *internal.OrgDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update an organization",
		Long:  "Update an organization's properties.",
		Args:  cobra.ExactArgs(1),
		Example: `  # Rename an organization
  loco org update my-org --new-name new-org-name`,
	}

	cmd.Flags().String("new-name", "", "New name for the organization")

	if deps != nil {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			runner := &updateRunner{deps: deps, name: args[0]}
			return runner.Run(cmd)
		}
	}

	return cmd
}

func (r *updateRunner) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	newName, _ := cmd.Flags().GetString("new-name")
	if newName == "" {
		return fmt.Errorf("at least one update flag is required (e.g., --new-name)")
	}

	// First, get the org by name to get its ID
	getReq := connect.NewRequest(&orgv1.GetOrgRequest{
		Key: &orgv1.GetOrgRequest_OrgName{OrgName: r.name},
	})
	getReq.Header().Set("Authorization", r.deps.AuthHeader())

	getResp, err := r.deps.GetOrg(ctx, getReq)
	if err != nil {
		return fmt.Errorf("failed to find organization %q: %w", r.name, err)
	}

	updateReq := connect.NewRequest(&orgv1.UpdateOrgRequest{
		OrgId:      getResp.Msg.Organization.Id,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		Name:       &newName,
	})
	updateReq.Header().Set("Authorization", r.deps.AuthHeader())

	_, err = r.deps.UpdateOrg(ctx, updateReq)
	if err != nil {
		return fmt.Errorf("failed to update organization: %w", err)
	}

	fmt.Fprintf(r.deps.Stdout, "Organization updated successfully.\n")

	return nil
}
