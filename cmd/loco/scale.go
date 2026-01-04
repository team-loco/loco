package loco

import (
	"context"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/ui"
	"github.com/team-loco/loco/shared"
	resourcev1 "github.com/team-loco/loco/shared/proto/resource/v1"
	"github.com/team-loco/loco/shared/proto/resource/v1/resourcev1connect"
)

var scaleCmd = &cobra.Command{
	Use:   "scale",
	Short: "Scale an application's resources",
	Long:  "Scale an application's resources, such as replicas, CPU, or memory.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return scaleCmdFunc(cmd)
	},
}

func init() {
	scaleCmd.Flags().StringP("app", "a", "", "Application name to scale")
	scaleCmd.Flags().String("org", "", "organization ID")
	scaleCmd.Flags().String("workspace", "", "workspace ID")
	scaleCmd.Flags().Int32P("replicas", "r", -1, "The number of replicas to scale to")
	scaleCmd.Flags().String("cpu", "", "The CPU to scale to (e.g. 100m, 0.5)")
	scaleCmd.Flags().String("memory", "", "The memory to scale to (e.g. 128Mi, 1Gi)")
	scaleCmd.Flags().String("host", "", "Set the host URL")
}

func scaleCmdFunc(cmd *cobra.Command) error {
	ctx := context.Background()

	host, err := getHost(cmd)
	if err != nil {
		return err
	}

	workspaceID, err := getWorkspaceId(cmd)
	if err != nil {
		return err
	}

	appName, err := cmd.Flags().GetString("app")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFlagParsing, err)
	}
	if appName == "" {
		return fmt.Errorf("app name is required. Use --app flag")
	}

	replicas, err := cmd.Flags().GetInt32("replicas")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFlagParsing, err)
	}

	cpu, err := cmd.Flags().GetString("cpu")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFlagParsing, err)
	}

	memory, err := cmd.Flags().GetString("memory")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFlagParsing, err)
	}

	if replicas == -1 && cpu == "" && memory == "" {
		return fmt.Errorf("at least one of --replicas, --cpu, or --memory must be provided")
	}

	if replicas != -1 && replicas < 1 {
		return fmt.Errorf("replicas must be >= 1")
	}

	locoToken, err := getLocoToken()
	if err != nil {
		return ErrLoginRequired
	}

	resourceClient := resourcev1connect.NewResourceServiceClient(shared.NewHTTPClient(), host)

	slog.Debug("fetching app by name", "workspaceId", workspaceID, "app_name", appName)

	getAppByNameReq := connect.NewRequest(&resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_NameKey{
			NameKey: &resourcev1.GetResourceNameKey{
				WorkspaceId: workspaceID,
				Name:        appName,
			},
		},
	})
	getAppByNameReq.Header().Set("Authorization", fmt.Sprintf("Bearer %s", locoToken.Token))

	getAppByNameResp, err := resourceClient.GetResource(ctx, getAppByNameReq)
	if err != nil {
		slog.Debug("failed to get app by name", "error", err)
		return fmt.Errorf("failed to get app '%s': %w", appName, err)
	}

	appID := getAppByNameResp.Msg.Id
	slog.Debug("found app by name", "app_name", appName, "app_id", appID)

	apiClient := client.NewClient(host, locoToken.Token)

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

	slog.Debug("scaling app", "app_id", appID, "app_name", appName)

	err = apiClient.ScaleApp(ctx, appID, replicasPtr, cpuPtr, memoryPtr)
	if err != nil {
		slog.Error("failed to scale app", "error", err)
		return fmt.Errorf("failed to scale app '%s': %w", appName, err)
	}

	s := lipgloss.NewStyle().
		Bold(true).
		Foreground(ui.LocoLightGreen).
		Render(fmt.Sprintf("\n🎉 Scaled application %s:", appName))
	fmt.Print(s)

	if replicas != -1 {
		fmt.Printf("\n  Replicas: %d", replicas)
	}
	if cpu != "" {
		fmt.Printf("\n  CPU: %s", cpu)
	}
	if memory != "" {
		fmt.Printf("\n  Memory: %s", memory)
	}
	fmt.Println()

	return nil
}
