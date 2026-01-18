package org

import (
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/org/internal"
	orgv1 "github.com/team-loco/loco/shared/proto/loco/org/v1"
)

type createRunner struct {
	deps *internal.OrgDeps
	name string
}

func buildCreateCmd(deps *internal.OrgDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create an organization",
		Long:  "Create a new organization. If name is not provided, a default name will be generated.",
		Args:  cobra.MaximumNArgs(1),
		Example: `  # Create an organization with a specific name
  loco org create my-company

  # Create an organization with an auto-generated name
  loco org create`,
	}

	if deps != nil {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			var name string
			if len(args) > 0 {
				name = args[0]
			}
			runner := &createRunner{deps: deps, name: name}
			return runner.Run(cmd)
		}
	}

	return cmd
}

func (r *createRunner) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	createReq := &orgv1.CreateOrgRequest{}
	if r.name != "" {
		createReq.Name = &r.name
	}

	req := connect.NewRequest(createReq)
	req.Header().Set("Authorization", r.deps.AuthHeader())

	resp, err := r.deps.CreateOrg(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to create organization: %w", err)
	}

	fmt.Fprintf(r.deps.Stdout, "Organization created successfully (ID: %d)\n", resp.Msg.OrgId)

	return nil
}
