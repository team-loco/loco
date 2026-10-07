package resource

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/infra"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/ui"
)

type scaleDeps struct {
	NewResourceClient func(host string) resourcev1connect.ResourceServiceClient
	Stdout            io.Writer
}

func buildScaleCmd() *cobra.Command {
	deps := scaleDeps{
		NewResourceClient: func(host string) resourcev1connect.ResourceServiceClient {
			return resourcev1connect.NewResourceServiceClient(httputil.NewHTTPClient(), host)
		},
		Stdout: os.Stdout,
	}
	return newScaleCmd(deps)
}

func newScaleCmd(deps scaleDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scale <name>",
		Short: "Scale a service's resources",
		Long: `Scale a service's replicas, CPU, or memory.

Examples:
  loco resource scale myapp --replicas 3
  loco resource scale myapp --cpu 0.5 --memory 512Mi
  loco resource scale myapp --replicas 2 --cpu 0.25`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]

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

			selected, err := infra.ResolveTarget(cmd)
			if err != nil {
				return err
			}

			// Create resource client
			resourceClient := deps.NewResourceClient(selected.Host)
			authHeader := fmt.Sprintf("Bearer %s", selected.Token)

			// Get resource by name
			getReq := connect.NewRequest(&resourcev1.GetResourceRequest{
				Key: &resourcev1.GetResourceRequest_NameKey{
					NameKey: &resourcev1.GetResourceNameKey{
						WorkspaceId:   selected.WorkspaceID,
						EnvironmentId: selected.EnvironmentID,
						Name:          name,
					},
				},
			})
			getReq.Header().Set("Authorization", authHeader)

			resourceResp, err := resourceClient.GetResource(ctx, getReq)
			if err != nil {
				return fmt.Errorf("service '%s' not found: %w", name, err)
			}

			resource := resourceResp.Msg.Resource
			slog.Debug("scaling service", "resource_id", resource.Id, "name", name)

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

			scaleReq := connect.NewRequest(&resourcev1.ScaleResourceRequest{
				ResourceId: resource.Id,
				Replicas:   replicasPtr,
				Cpu:        cpuPtr,
				Memory:     memoryPtr,
			})
			scaleReq.Header().Set("Authorization", authHeader)

			_, err = resourceClient.ScaleResource(ctx, scaleReq)
			if err != nil {
				slog.Error("failed to scale service", "error", err)
				return fmt.Errorf("failed to scale service '%s': %w", name, err)
			}

			// Success message
			s := lipgloss.NewStyle().
				Bold(true).
				Foreground(ui.Ok).
				Render(fmt.Sprintf("\n🎉 Scaled service %s:", name))
			fmt.Fprintln(deps.Stdout, s)

			if replicas != -1 {
				fmt.Fprintf(deps.Stdout, "  Replicas: %d\n", replicas)
			}
			if cpu != "" {
				fmt.Fprintf(deps.Stdout, "  CPU: %s\n", cpu)
			}
			if memory != "" {
				fmt.Fprintf(deps.Stdout, "  Memory: %s\n", memory)
			}

			return nil
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name or ID")
	cmd.Flags().String("environment", "", "Environment name or ID")
	cmd.Flags().Int32P("replicas", "r", -1, "Number of replicas to scale to")
	cmd.Flags().String("cpu", "", "CPU to scale to (e.g. 100m, 0.5)")
	cmd.Flags().String("memory", "", "Memory to scale to (e.g. 128Mi, 1Gi)")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}
