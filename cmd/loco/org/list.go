package org

import (
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/org/internal"
	orgv1 "github.com/team-loco/loco/shared/proto/loco/org/v1"
	userv1 "github.com/team-loco/loco/shared/proto/loco/user/v1"
)

type listRunner struct {
	deps *internal.OrgDeps
}

func buildListCmd(deps *internal.OrgDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List organizations",
		Long:  "List all organizations you have access to.",
		Args:  cobra.NoArgs,
		Example: `  # List all your organizations
  loco org list`,
	}

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

	// Get current user ID via WhoAmI
	whoAmIReq := connect.NewRequest(&userv1.WhoAmIRequest{})
	whoAmIReq.Header().Set("Authorization", r.deps.AuthHeader())

	whoAmIResp, err := r.deps.WhoAmI(ctx, whoAmIReq)
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	req := connect.NewRequest(&orgv1.ListUserOrgsRequest{
		UserId: whoAmIResp.Msg.User.Id,
	})
	req.Header().Set("Authorization", r.deps.AuthHeader())

	resp, err := r.deps.ListUserOrgs(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to list organizations: %w", err)
	}

	if len(resp.Msg.Orgs) == 0 {
		fmt.Fprintln(r.deps.Stdout, "No organizations found.")
		return nil
	}

	fmt.Fprintln(r.deps.Stdout, "Organizations:")
	for _, org := range resp.Msg.Orgs {
		fmt.Fprintf(r.deps.Stdout, "  - %s (ID: %d)\n", org.Name, org.Id)
	}

	return nil
}
