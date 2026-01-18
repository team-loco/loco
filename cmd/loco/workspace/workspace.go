package workspace

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/workspace/internal"
)

type depsKey struct{}

func withDeps(ctx context.Context, deps *internal.WorkspaceDeps) context.Context {
	return context.WithValue(ctx, depsKey{}, deps)
}

func getDeps(ctx context.Context) *internal.WorkspaceDeps {
	deps, _ := ctx.Value(depsKey{}).(*internal.WorkspaceDeps)
	return deps
}

// BuildWorkspaceCmd creates the "workspace" parent command with all subcommands.
func BuildWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workspace",
		Aliases: []string{"ws"},
		Short:   "Manage workspaces",
		Long:    "Commands for creating, listing, and managing workspaces.",
		Example: `  # List all workspaces
  loco workspace list

  # List workspaces in a specific organization
  loco workspace list --org-id 123

  # Create a new workspace
  loco workspace create my-workspace --org-id 123

  # Update a workspace
  loco workspace update 456 --name new-name

  # Delete a workspace
  loco workspace delete 456`,
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

			deps := internal.NewWorkspaceDeps(host, locoToken.Token)
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

// BuildWorkspaceCmdWithDeps creates the workspace command with pre-built deps (for testing).
func BuildWorkspaceCmdWithDeps(deps *internal.WorkspaceDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workspace",
		Aliases: []string{"ws"},
		Short:   "Manage workspaces",
		Long:    "Commands for creating, listing, and managing workspaces.",
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
		name := args[0]
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
		runner := &deleteRunner{deps: deps, id: 0} // ID will be parsed in Run
		return runner.Run(cmd, args)
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
		runner := &updateRunner{deps: deps, id: 0} // ID will be parsed in Run
		return runner.Run(cmd, args)
	}
	return cmd
}
