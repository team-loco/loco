package org

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/org/internal"
)

type depsKey struct{}

func withDeps(ctx context.Context, deps *internal.OrgDeps) context.Context {
	return context.WithValue(ctx, depsKey{}, deps)
}

func getDeps(ctx context.Context) *internal.OrgDeps {
	deps, _ := ctx.Value(depsKey{}).(*internal.OrgDeps)
	return deps
}

// BuildOrgCmd creates the "org" parent command with all subcommands.
func BuildOrgCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "org",
		Short: "Manage organizations",
		Long:  "Commands for creating, listing, and managing organizations.",
		Example: `  # List all organizations
  loco org list

  # Create a new organization
  loco org create my-org

  # Rename an organization
  loco org update my-org --new-name new-org

  # Delete an organization
  loco org delete my-org`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Name() == "help" || cmd.Flags().Changed("help") {
				return nil
			}

			host, err := internal.GetHost(cmd)
			if err != nil {
				return err
			}

			locoToken, err := internal.GetCurrentLocoToken()
			if err != nil {
				return fmt.Errorf("login required - please run 'loco login'")
			}

			deps := internal.NewOrgDeps(host, locoToken.Token)
			cmd.SetContext(withDeps(cmd.Context(), deps))

			return nil
		},
	}

	cmd.AddCommand(
		buildLazyListCmd(),
		buildLazyCreateCmd(),
		buildLazyDeleteCmd(),
		buildLazyUpdateCmd(),
	)

	return cmd
}

// BuildOrgCmdWithDeps creates the org command with pre-built deps (for testing).
func BuildOrgCmdWithDeps(deps *internal.OrgDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "org",
		Short: "Manage organizations",
		Long:  "Commands for creating, listing, and managing organizations.",
	}

	cmd.AddCommand(
		buildListCmd(deps),
		buildCreateCmd(deps),
		buildDeleteCmd(deps),
		buildUpdateCmd(deps),
	)

	return cmd
}

// Lazy command builders

func buildLazyListCmd() *cobra.Command {
	cmd := buildListCmd(nil)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		deps := getDeps(cmd.Context())
		if deps == nil {
			return fmt.Errorf("internal error: deps not initialized")
		}
		runner := &listRunner{deps: deps}
		return runner.Run(cmd)
	}
	return cmd
}

func buildLazyCreateCmd() *cobra.Command {
	cmd := buildCreateCmd(nil)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		deps := getDeps(cmd.Context())
		if deps == nil {
			return fmt.Errorf("internal error: deps not initialized")
		}
		var name string
		if len(args) > 0 {
			name = args[0]
		}
		runner := &createRunner{deps: deps, name: name}
		return runner.Run(cmd)
	}
	return cmd
}

func buildLazyDeleteCmd() *cobra.Command {
	cmd := buildDeleteCmd(nil)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		deps := getDeps(cmd.Context())
		if deps == nil {
			return fmt.Errorf("internal error: deps not initialized")
		}
		name := args[0]
		runner := &deleteRunner{deps: deps, name: name}
		return runner.Run(cmd)
	}
	return cmd
}

func buildLazyUpdateCmd() *cobra.Command {
	cmd := buildUpdateCmd(nil)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		deps := getDeps(cmd.Context())
		if deps == nil {
			return fmt.Errorf("internal error: deps not initialized")
		}
		name := args[0]
		runner := &updateRunner{deps: deps, name: name}
		return runner.Run(cmd)
	}
	return cmd
}
