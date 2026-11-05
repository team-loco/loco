package resource

import (
	"errors"
	"fmt"
	"log/slog"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/ui"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

var (
	errNoScaleFlags   = errors.New("at least one of --replicas, --cpu, or --memory must be provided")
	errReplicasTooLow = errors.New("replicas must be >= 1")
)

func newScaleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scale <name>",
		Short: "Scale a service's resources in one environment",
		Long: `Scale a service's replicas, CPU, or memory in one environment.

--env or LOCO_ENV names the environment; a workspace with a single environment uses it.

Examples:
  loco resource scale myapp --env production --replicas 3
  loco resource scale myapp --env staging --cpu 0.5 --memory 512Mi
  loco resource scale myapp --env production --replicas 2 --cpu 0.25`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]

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

			if replicas == -1 && cpu == "" && memory == "" {
				return errNoScaleFlags
			}

			if replicas != -1 && replicas < 1 {
				return errReplicasTooLow
			}

			t, err := cmdutil.ResolveTarget(ctx, cmd)
			if err != nil {
				return err
			}
			t, err = t.WithEnvironment(ctx, cmd, false)
			if err != nil {
				return err
			}
			env := t.Environment
			resource, err := t.Service(ctx, name)
			if err != nil {
				return err
			}
			slog.Debug("scaling service", "resource_id", resource.GetId(), "name", name, "environment", env.GetName())

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

			scaleReq := &resourcev1.ScaleResourceRequest{
				ResourceId:    resource.GetId(),
				EnvironmentId: env.GetId(),
				Replicas:      replicasPtr,
				Cpu:           cpuPtr,
				Memory:        memoryPtr,
			}

			_, err = t.Clients.Resource.ScaleResource(ctx, scaleReq)
			if err != nil {
				return fmt.Errorf("failed to scale service '%s': %w", name, err)
			}

			s := lipgloss.NewStyle().
				Bold(true).
				Foreground(ui.Ok).
				Render(fmt.Sprintf("\n🎉 Scaled service %s in %s:", name, env.GetName()))
			fmt.Fprintln(cmd.OutOrStdout(), s)

			if replicas != -1 {
				fmt.Fprintf(cmd.OutOrStdout(), "  Replicas: %d\n", replicas)
			}
			if cpu != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  CPU: %s\n", cpu)
			}
			if memory != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Memory: %s\n", memory)
			}

			return nil
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().String("env", "", "Environment name (defaults to the workspace's only environment)")
	cmd.Flags().Int32P("replicas", "r", -1, "Number of replicas to scale to")
	cmd.Flags().String("cpu", "", "CPU to scale to (e.g. 100m, 0.5)")
	cmd.Flags().String("memory", "", "Memory to scale to (e.g. 128Mi, 1Gi)")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}
