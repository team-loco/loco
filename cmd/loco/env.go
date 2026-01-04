package loco

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"strings"

	"connectrpc.com/connect"
	"github.com/charmbracelet/lipgloss"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/ui"
	"github.com/team-loco/loco/shared"
	resourcev1 "github.com/team-loco/loco/shared/proto/resource/v1"
	"github.com/team-loco/loco/shared/proto/resource/v1/resourcev1connect"
)

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Sync environment variables for an application",
	Long:  "Sync environment variables for an application without redeploying.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return envCmdFunc(cmd)
	},
}

func init() {
	envCmd.Flags().StringP("app", "a", "", "Application name")
	envCmd.Flags().String("org", "", "organization ID")
	envCmd.Flags().String("workspace", "", "workspace ID")
	envCmd.Flags().String("env-file", "", "path to .env file")
	envCmd.Flags().StringSlice("set", []string{}, "set environment variables (e.g. --set KEY1=VALUE1 --set KEY2=VALUE2)")
	envCmd.Flags().String("host", "", "Set the host URL")
}

func envCmdFunc(cmd *cobra.Command) error {
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

	envFile, err := cmd.Flags().GetString("env-file")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFlagParsing, err)
	}

	setVars, err := cmd.Flags().GetStringSlice("set")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFlagParsing, err)
	}

	envVars := make(map[string]string)

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

	for _, setVar := range setVars {
		parts := strings.SplitN(setVar, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid --set format: %s, expected KEY=VALUE", setVar)
		}
		envVars[parts[0]] = parts[1]
	}

	if len(envVars) == 0 {
		return fmt.Errorf("no environment variables to sync. Use --env-file or --set")
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

	apiClient := client.NewClient(host, locoToken.Token)

	slog.Debug("updating environment variables", "app_id", appID, "app_name", appName)

	err = apiClient.UpdateAppEnv(ctx, appID, envVars)
	if err != nil {
		slog.Error("failed to update environment variables", "error", err)
		return fmt.Errorf("failed to update environment variables for app '%s': %w", appName, err)
	}

	s := lipgloss.NewStyle().
		Bold(true).
		Foreground(ui.LocoLightGreen).
		Render(fmt.Sprintf("\n🎉 Environment variables synced for application %s", appName))
	fmt.Println(s)

	return nil
}
