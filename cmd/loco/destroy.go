package loco

import (
	"context"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/ui"
	"github.com/team-loco/loco/shared"
	resourcev1 "github.com/team-loco/loco/shared/proto/resource/v1"
	"github.com/team-loco/loco/shared/proto/resource/v1/resourcev1connect"
)

func init() {
	destroyCmd.Flags().StringP("app", "a", "", "Application name to destroy")
	destroyCmd.Flags().String("org", "", "organization ID")
	destroyCmd.Flags().String("workspace", "", "workspace ID")
	destroyCmd.Flags().BoolP("yes", "y", false, "Assume yes to all prompts")
	destroyCmd.Flags().String("host", "", "Set the host URL")
}

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Destroy an application deployment",
	RunE: func(cmd *cobra.Command, args []string) error {
		return destroyCmdFunc(cmd)
	},
}

func destroyCmdFunc(cmd *cobra.Command) error {
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

	yes, err := cmd.Flags().GetBool("yes")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFlagParsing, err)
	}

	locoToken, err := getLocoToken()
	if err != nil {
		return ErrLoginRequired
	}

	resourceClient := resourcev1connect.NewResourceServiceClient(shared.NewHTTPClient(), host)

	slog.Debug("fetching app by name", "workspaceId", workspaceID, "app_name", appName)

	getAppByNameReq := connect.NewRequest(&resourcev1.GetResourceRequest{
		WorkspaceId: &workspaceID,
		Name:        &appName,
	})
	getAppByNameReq.Header().Set("Authorization", fmt.Sprintf("Bearer %s", locoToken.Token))

	getAppByNameResp, err := resourceClient.GetResource(ctx, getAppByNameReq)
	if err != nil {
		slog.Debug("failed to get app by name", "error", err)
		return fmt.Errorf("failed to get app '%s': %w", appName, err)
	}

	appID := getAppByNameResp.Msg.Id
	slog.Debug("found app by name", "app_name", appName, "app_id", appID)

	if !yes {
		confirmed, confirmErr := ui.AskYesNo(fmt.Sprintf("Are you sure you want to destroy the app '%s'?", appName))
		if confirmErr != nil {
			return confirmErr
		}
		if !confirmed {
			fmt.Println("Aborted.")
			return nil
		}
	}

	slog.Debug("destroying app", "app_id", appID, "app_name", appName)

	destroyReq := connect.NewRequest(&resourcev1.DeleteResourceRequest{
		ResourceId: appID,
	})
	destroyReq.Header().Set("Authorization", fmt.Sprintf("Bearer %s", locoToken.Token))

	_, err = resourceClient.DeleteResource(ctx, destroyReq)
	if err != nil {
		slog.Error("failed to destroy app", "error", err)
		return fmt.Errorf("failed to destroy app '%s': %w", appName, err)
	}

	successMsg := fmt.Sprintf("\n🎉 App '%s' destroyed!", appName)
	s := lipgloss.NewStyle().
		Bold(true).
		Foreground(ui.LocoLightGreen).
		Render(successMsg)

	fmt.Println(s)

	return nil
}
