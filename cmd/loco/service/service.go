package service

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/service/internal"
)

type depsKey struct{}

// withDeps stores ServiceDeps in context.
func withDeps(ctx context.Context, deps *internal.ServiceDeps) context.Context {
	return context.WithValue(ctx, depsKey{}, deps)
}

// getDeps retrieves ServiceDeps from context.
func getDeps(ctx context.Context) *internal.ServiceDeps {
	deps, _ := ctx.Value(depsKey{}).(*internal.ServiceDeps)
	return deps
}

// BuildServiceCmd creates the "service" parent command with all subcommands.
// It lazily initializes ServiceDeps when a subcommand is run.
func BuildServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage services",
		Long:  "Commands for deploying, scaling, and managing services on Loco.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Skip auth for help commands
			if cmd.Name() == "help" || cmd.Flags().Changed("help") {
				return nil
			}

			// Get host and token, then build deps
			host, err := internal.GetHost(cmd)
			if err != nil {
				return err
			}

			locoToken, err := internal.GetCurrentLocoToken()
			if err != nil {
				return fmt.Errorf("login required - please run 'loco login'")
			}

			// Build deps and store in context
			deps := internal.NewServiceDeps(host, locoToken.Token)
			cmd.SetContext(withDeps(cmd.Context(), deps))

			return nil
		},
	}

	// Add subcommands - they'll get deps from context at runtime
	cmd.AddCommand(
		buildLazyDeployCmd(),
		buildLazyScaleCmd(),
		buildLazyStatusCmd(),
		buildLazyLogsCmd(),
		buildLazyEventsCmd(),
		buildLazyEnvCmd(),
		buildLazyDestroyCmd(),
	)

	return cmd
}

// BuildServiceCmdWithDeps creates the service command with pre-built deps (for testing).
func BuildServiceCmdWithDeps(deps *internal.ServiceDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage services",
		Long:  "Commands for deploying, scaling, and managing services on Loco.",
	}

	cmd.AddCommand(
		buildDeployCmd(deps),
		buildScaleCmd(deps),
		buildStatusCmd(deps),
		buildLogsCmd(deps),
		buildEventsCmd(deps),
		buildEnvCmd(deps),
		buildDestroyCmd(deps),
	)

	return cmd
}

// Lazy command builders that get deps from context

func buildLazyDeployCmd() *cobra.Command {
	cmd := buildDeployCmd(nil)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		deps := getDeps(cmd.Context())
		if deps == nil {
			return fmt.Errorf("internal error: deps not initialized")
		}
		// Recreate runner with deps
		name := args[0]
		runner := &deployRunner{deps: deps, name: name}
		return runner.Run(cmd)
	}
	return cmd
}

func buildLazyScaleCmd() *cobra.Command {
	cmd := buildScaleCmd(nil)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		deps := getDeps(cmd.Context())
		if deps == nil {
			return fmt.Errorf("internal error: deps not initialized")
		}
		name := args[0]
		runner := &scaleRunner{deps: deps, name: name}
		return runner.Run(cmd)
	}
	return cmd
}

func buildLazyStatusCmd() *cobra.Command {
	cmd := buildStatusCmd(nil)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		deps := getDeps(cmd.Context())
		if deps == nil {
			return fmt.Errorf("internal error: deps not initialized")
		}
		name := args[0]
		runner := &statusRunner{deps: deps, name: name}
		return runner.Run(cmd)
	}
	return cmd
}

func buildLazyLogsCmd() *cobra.Command {
	cmd := buildLogsCmd(nil)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		deps := getDeps(cmd.Context())
		if deps == nil {
			return fmt.Errorf("internal error: deps not initialized")
		}
		name := args[0]
		runner := &logsRunner{deps: deps, name: name}
		return runner.Run(cmd)
	}
	return cmd
}

func buildLazyEventsCmd() *cobra.Command {
	cmd := buildEventsCmd(nil)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		deps := getDeps(cmd.Context())
		if deps == nil {
			return fmt.Errorf("internal error: deps not initialized")
		}
		name := args[0]
		runner := &eventsRunner{deps: deps, name: name}
		return runner.Run(cmd)
	}
	return cmd
}

func buildLazyEnvCmd() *cobra.Command {
	cmd := buildEnvCmd(nil)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		deps := getDeps(cmd.Context())
		if deps == nil {
			return fmt.Errorf("internal error: deps not initialized")
		}
		name := args[0]
		runner := &envRunner{deps: deps, name: name, setArgs: args[1:]}
		return runner.Run(cmd)
	}
	return cmd
}

func buildLazyDestroyCmd() *cobra.Command {
	cmd := buildDestroyCmd(nil)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		deps := getDeps(cmd.Context())
		if deps == nil {
			return fmt.Errorf("internal error: deps not initialized")
		}
		name := args[0]
		runner := &destroyRunner{deps: deps, name: name}
		return runner.Run(cmd)
	}
	return cmd
}
