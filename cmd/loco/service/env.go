package service

import (
	"fmt"
	"log/slog"
	"maps"
	"os"
	"strings"

	"connectrpc.com/connect"
	"github.com/charmbracelet/lipgloss"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/service/internal"
	"github.com/team-loco/loco/internal/ui"
	resourcev1 "github.com/team-loco/loco/shared/proto/loco/resource/v1"
)

func buildEnvCmd(deps *internal.ServiceDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env <name>",
		Short: "Manage service environment variables",
		Long: `Manage environment variables for a service.

Examples:
  loco service env myapp set KEY=VALUE
  loco service env myapp set KEY1=VALUE1 KEY2=VALUE2
  loco service env myapp --env-file .env
  loco service env myapp --set KEY=VALUE --env-file .env`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			runner := &envRunner{
				deps:    deps,
				name:    name,
				setArgs: args[1:], // Remaining args are KEY=VALUE pairs
			}
			return runner.Run(cmd)
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().String("env-file", "", "Path to .env file")
	cmd.Flags().StringSlice("set", []string{}, "Set environment variables (e.g. --set KEY1=VALUE1 --set KEY2=VALUE2)")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}

type envRunner struct {
	deps    *internal.ServiceDeps
	name    string
	setArgs []string
}

func (r *envRunner) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	envFile, err := cmd.Flags().GetString("env-file")
	if err != nil {
		return fmt.Errorf("error reading env-file flag: %w", err)
	}

	setVars, err := cmd.Flags().GetStringSlice("set")
	if err != nil {
		return fmt.Errorf("error reading set flag: %w", err)
	}

	// Build env vars map
	envVars := make(map[string]string)

	// Load from file first
	if envFile != "" {
		f, openErr := os.Open(envFile)
		if openErr != nil {
			return fmt.Errorf("failed to open env file %s: %w", envFile, openErr)
		}
		defer f.Close()
		parsed, parseErr := godotenv.Parse(f)
		if parseErr != nil {
			return fmt.Errorf("failed to parse env file %s: %w", envFile, parseErr)
		}
		maps.Copy(envVars, parsed)
	}

	// Parse --set flags
	for _, setVar := range setVars {
		parts := strings.SplitN(setVar, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid --set format: %s, expected KEY=VALUE", setVar)
		}
		envVars[parts[0]] = parts[1]
	}

	// Parse positional KEY=VALUE args
	for _, arg := range r.setArgs {
		parts := strings.SplitN(arg, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid format: %s, expected KEY=VALUE", arg)
		}
		envVars[parts[0]] = parts[1]
	}

	if len(envVars) == 0 {
		return fmt.Errorf("no environment variables to sync. Use positional args (KEY=VALUE), --set, or --env-file")
	}

	// Resolve resource
	resolver := internal.NewContextResolver(r.deps)
	resource, err := resolver.ResolveResourceByName(ctx, cmd, r.name)
	if err != nil {
		return err
	}

	slog.Debug("updating environment variables", "resource_id", resource.Id, "name", r.name)

	req := connect.NewRequest(&resourcev1.UpdateResourceEnvRequest{
		ResourceId: resource.Id,
		Env:        envVars,
	})
	req.Header().Set("Authorization", r.deps.AuthHeader())

	_, err = r.deps.UpdateResourceEnv(ctx, req)
	if err != nil {
		slog.Error("failed to update environment variables", "error", err)
		return fmt.Errorf("failed to update environment variables for service '%s': %w", r.name, err)
	}

	s := lipgloss.NewStyle().
		Bold(true).
		Foreground(ui.LocoLightGreen).
		Render(fmt.Sprintf("\n🎉 Environment variables synced for service %s", r.name))
	fmt.Fprintln(r.deps.Stdout, s)

	return nil
}
