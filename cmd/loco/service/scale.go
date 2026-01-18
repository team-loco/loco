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

func buildScaleCmd(deps *internal.ServiceDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scale <name>",
		Short: "Scale a service's resources",
		Long: `Scale a service's replicas, CPU, or memory.

Examples:
  loco service scale myapp --replicas 3
  loco service scale myapp --cpu 0.5 --memory 512Mi
  loco service scale myapp --replicas 2 --cpu 0.25`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			runner := &scaleRunner{
				deps: deps,
				name: name,
			}
			return runner.Run(cmd)
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().Int32P("replicas", "r", -1, "Number of replicas to scale to")
	cmd.Flags().String("cpu", "", "CPU to scale to (e.g. 100m, 0.5)")
	cmd.Flags().String("memory", "", "Memory to scale to (e.g. 128Mi, 1Gi)")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}

type scaleRunner struct {
	deps *internal.ServiceDeps
	name string
}

func (r *scaleRunner) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	// Parse flags
	replicas, err := cmd.Flags().GetInt32("replicas")
	if err != nil {
		return fmt.Errorf("error reading replicas flag: %w", err)
	}

	cpu, err := cmd.Flags().GetString("cpu")
	if err != nil {
		return fmt.Errorf("error reading cpu flag: %w", err)
	}

	memory, err := cmd.Flags().GetString("memory")
	if err != nil {
		return fmt.Errorf("error reading memory flag: %w", err)
	}

	// Validate at least one scaling parameter
	if replicas == -1 && cpu == "" && memory == "" {
		return fmt.Errorf("at least one of --replicas, --cpu, or --memory must be provided")
	}

	if replicas != -1 && replicas < 1 {
		return fmt.Errorf("replicas must be >= 1")
	}

	// Resolve resource
	resolver := internal.NewContextResolver(r.deps)
	resource, err := resolver.ResolveResourceByName(ctx, cmd, r.name)
	if err != nil {
		return err
	}

	slog.Debug("scaling service", "resource_id", resource.Id, "name", r.name)

	// Build scale request
	var replicasPtr *int32
	if replicas != -1 {
		replicasPtr = &replicas
	}

	var cpuPtr *string
	if cpu != "" {
		cpuPtr = &cpu
	}

	var memoryPtr *string
	if memory != "" {
		memoryPtr = &memory
	}

	req := connect.NewRequest(&resourcev1.ScaleResourceRequest{
		ResourceId: resource.Id,
		Replicas:   replicasPtr,
		Cpu:        cpuPtr,
		Memory:     memoryPtr,
	})
	req.Header().Set("Authorization", r.deps.AuthHeader())

	_, err = r.deps.ScaleResource(ctx, req)
	if err != nil {
		slog.Error("failed to scale service", "error", err)
		return fmt.Errorf("failed to scale service '%s': %w", r.name, err)
	}

	// Success message
	s := lipgloss.NewStyle().
		Bold(true).
		Foreground(ui.LocoLightGreen).
		Render(fmt.Sprintf("\n🎉 Scaled service %s:", r.name))
	fmt.Fprintln(r.deps.Stdout, s)

	if replicas != -1 {
		fmt.Fprintf(r.deps.Stdout, "  Replicas: %d\n", replicas)
	}
	if cpu != "" {
		fmt.Fprintf(r.deps.Stdout, "  CPU: %s\n", cpu)
	}
	if memory != "" {
		fmt.Fprintf(r.deps.Stdout, "  Memory: %s\n", memory)
	}

	return nil
}
