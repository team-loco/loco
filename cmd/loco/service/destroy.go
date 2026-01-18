package service

import (
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/service/internal"
	"github.com/team-loco/loco/internal/ui"
	resourcev1 "github.com/team-loco/loco/shared/proto/loco/resource/v1"
)

func buildDestroyCmd(deps *internal.ServiceDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy <name>",
		Short: "Destroy a service",
		Long: `Destroy a service and all its resources.

Examples:
  loco service destroy myapp
  loco service destroy myapp --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			runner := &destroyRunner{
				deps: deps,
				name: name,
			}
			return runner.Run(cmd)
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}

type destroyRunner struct {
	deps *internal.ServiceDeps
	name string
}

func (r *destroyRunner) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	yes, err := cmd.Flags().GetBool("yes")
	if err != nil {
		return fmt.Errorf("error reading yes flag: %w", err)
	}

	// Resolve resource
	resolver := internal.NewContextResolver(r.deps)
	resource, err := resolver.ResolveResourceByName(ctx, cmd, r.name)
	if err != nil {
		return err
	}

	// Confirmation prompt
	if !yes {
		confirmed, confirmErr := ui.AskYesNo(fmt.Sprintf("Are you sure you want to destroy the service '%s'?", r.name))
		if confirmErr != nil {
			return confirmErr
		}
		if !confirmed {
			fmt.Fprintln(r.deps.Stdout, "Aborted.")
			return nil
		}
	}

	slog.Debug("destroying service", "resource_id", resource.Id, "name", r.name)

	req := connect.NewRequest(&resourcev1.DeleteResourceRequest{
		ResourceId: resource.Id,
	})
	req.Header().Set("Authorization", r.deps.AuthHeader())

	_, err = r.deps.DeleteResource(ctx, req)
	if err != nil {
		slog.Error("failed to destroy service", "error", err)
		return fmt.Errorf("failed to destroy service '%s': %w", r.name, err)
	}

	successMsg := fmt.Sprintf("\n🎉 Service '%s' destroyed!", r.name)
	s := lipgloss.NewStyle().
		Bold(true).
		Foreground(ui.LocoLightGreen).
		Render(successMsg)

	fmt.Fprintln(r.deps.Stdout, s)

	return nil
}
