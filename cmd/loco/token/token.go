package token

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/token/internal"
)

type depsKey struct{}

func withDeps(ctx context.Context, deps *internal.TokenDeps) context.Context {
	return context.WithValue(ctx, depsKey{}, deps)
}

func getDeps(ctx context.Context) *internal.TokenDeps {
	deps, _ := ctx.Value(depsKey{}).(*internal.TokenDeps)
	return deps
}

// BuildTokenCmd creates the "token" parent command with all subcommands.
func BuildTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage API tokens",
		Long:  "Commands for creating, listing, and managing personal access tokens.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Name() == "help" || cmd.Flags().Changed("help") {
				return nil
			}

			// Commands that need API access
			needsAPI := cmd.Name() == "create" || cmd.Name() == "list" || cmd.Name() == "delete"

			if needsAPI {
				host, err := internal.GetHost(cmd)
				if err != nil {
					return err
				}

				locoToken, err := internal.GetCurrentLocoToken()
				if err != nil {
					return fmt.Errorf("login required - please run 'loco login'")
				}

				deps := internal.NewTokenDepsWithAPI(host, locoToken.Token)
				cmd.SetContext(withDeps(cmd.Context(), deps))
			} else {
				// show/revoke only need local keychain access
				deps := internal.NewTokenDeps()
				cmd.SetContext(withDeps(cmd.Context(), deps))
			}

			return nil
		},
	}

	cmd.AddCommand(
		buildLazyShowCmd(),
		buildLazyRevokeCmd(),
		buildLazyCreateCmd(),
		buildLazyListCmd(),
		buildLazyDeleteCmd(),
	)

	return cmd
}

// BuildTokenCmdWithDeps creates the token command with pre-built deps (for testing).
func BuildTokenCmdWithDeps(deps *internal.TokenDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage API tokens",
		Long:  "Commands for creating, listing, and managing personal access tokens.",
	}

	cmd.AddCommand(
		buildShowCmd(deps),
		buildRevokeCmd(deps),
		buildCreateCmd(deps),
		buildListCmd(deps),
		buildDeleteCmd(deps),
	)

	return cmd
}

// Lazy command builders

func buildLazyShowCmd() *cobra.Command {
	cmd := buildShowCmd(nil)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		deps := getDeps(cmd.Context())
		if deps == nil {
			return fmt.Errorf("internal error: deps not initialized")
		}
		runner := &showRunner{deps: deps}
		return runner.Run(cmd)
	}
	return cmd
}

func buildLazyRevokeCmd() *cobra.Command {
	cmd := buildRevokeCmd(nil)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		deps := getDeps(cmd.Context())
		if deps == nil {
			return fmt.Errorf("internal error: deps not initialized")
		}
		runner := &revokeRunner{deps: deps}
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
		runner := &createRunner{deps: deps, name: args[0]}
		return runner.Run(cmd)
	}
	return cmd
}

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

func buildLazyDeleteCmd() *cobra.Command {
	cmd := buildDeleteCmd(nil)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		deps := getDeps(cmd.Context())
		if deps == nil {
			return fmt.Errorf("internal error: deps not initialized")
		}
		runner := &deleteRunner{deps: deps, name: args[0]}
		return runner.Run(cmd)
	}
	return cmd
}
