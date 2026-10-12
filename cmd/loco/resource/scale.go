package resource

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/team-loco/loco/internal/rpc"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
)

var (
	errNoScaleFlags   = errors.New("at least one of --replicas, --cpu, or --memory must be provided")
	errReplicasTooLow = errors.New("replicas must be >= 1")
)

type scaleDeps struct {
	LoadSessionConfig func() (*session.SessionConfig, error)
	NewAPIClient      func(host, token string) *client.Client
	NewResourceClient func(host, token string) resourcev1connect.ResourceServiceClient
	Environments      func(host, token string) environmentv1connect.EnvironmentServiceClient
	Stdout            io.Writer
}

func buildScaleCmd() *cobra.Command {
	deps := scaleDeps{
		LoadSessionConfig: session.Load,
		NewAPIClient:      client.NewClient,
		NewResourceClient: func(host, token string) resourcev1connect.ResourceServiceClient {
			resourceClients := rpc.ForHost(host, token)
			return resourceClients.Resource
		},
		Environments: func(host, token string) environmentv1connect.EnvironmentServiceClient {
			environmentClients := rpc.ForHost(host, token)
			return environmentClients.Environment
		},
		Stdout: os.Stdout,
	}
	return newScaleCmd(deps)
}

func newScaleCmd(deps scaleDeps) *cobra.Command {
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
				return errNoScaleFlags
			}

			if replicas != -1 && replicas < 1 {
				return errReplicasTooLow
			}

			// Get host and token
			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}

			locoToken, err := cmdutil.GetCurrentLocoToken(cmd)
			if err != nil {
				return err
			}

			// Resolve workspace ID
			apiClient := deps.NewAPIClient(host, locoToken.Token)
			workspaceID, err := cmdutil.ResolveWorkspaceID(ctx, cmd, deps.LoadSessionConfig, apiClient)
			if err != nil {
				return err
			}

			resourceClient := deps.NewResourceClient(host, locoToken.Token)

			environments := deps.Environments(host, locoToken.Token)
			env, err := cmdutil.ResolveEnvironment(ctx, cmd, environments, nil, false, workspaceID)
			if err != nil {
				return err
			}

			// Get resource by name
			getReq := connect.NewRequest(&resourcev1.GetResourceRequest{
				Key: &resourcev1.GetResourceRequest_NameKey{
					NameKey: &resourcev1.GetResourceNameKey{
						WorkspaceId: workspaceID,
						Name:        name,
					},
				},
			})

			resourceResp, err := resourceClient.GetResource(ctx, getReq)
			if err != nil {
				return fmt.Errorf("service '%s' not found: %w", name, err)
			}

			resource := resourceResp.Msg.GetResource()
			slog.Debug("scaling service", "resource_id", resource.GetId(), "name", name, "environment", env.GetName())

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
				ResourceId:    resource.GetId(),
				EnvironmentId: env.GetId(),
				Replicas:      replicasPtr,
				Cpu:           cpuPtr,
				Memory:        memoryPtr,
			})

			_, err = resourceClient.ScaleResource(ctx, scaleReq)
			if err != nil {
				slog.Error("failed to scale service", "error", err)
				return fmt.Errorf("failed to scale service '%s': %w", name, err)
			}

			// Success message
			s := lipgloss.NewStyle().
				Bold(true).
				Foreground(ui.Ok).
				Render(fmt.Sprintf("\n🎉 Scaled service %s in %s:", name, env.GetName()))
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
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().String("env", "", "Environment name (defaults to the workspace's only environment)")
	cmd.Flags().Int32P("replicas", "r", -1, "Number of replicas to scale to")
	cmd.Flags().String("cpu", "", "CPU to scale to (e.g. 100m, 0.5)")
	cmd.Flags().String("memory", "", "Memory to scale to (e.g. 128Mi, 1Gi)")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}
