package loco

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"connectrpc.com/connect"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/ui"
	"github.com/team-loco/loco/shared"
	resourcev1 "github.com/team-loco/loco/shared/proto/resource/v1"
	"github.com/team-loco/loco/shared/proto/resource/v1/resourcev1connect"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show application status",
	RunE: func(cmd *cobra.Command, args []string) error {
		return statusCmdFunc(cmd)
	},
}

func statusCmdFunc(cmd *cobra.Command) error {
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

	output, err := cmd.Flags().GetString("output")
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
		logRequestID(ctx, err, "get app by name")
		return fmt.Errorf("failed to get app '%s': %w", appName, err)
	}

	appID := getAppByNameResp.Msg.Id
	slog.Debug("found app by name", "app_name", appName, "app_id", appID)

	apiClient := client.NewClient(host, locoToken.Token)

	slog.Debug("retrieving app status", "app_id", appID, "app_name", appName)

	statusResp, err := apiClient.GetAppStatus(ctx, appID)
	if err != nil {
		slog.Error("failed to get app status", "error", err)
		return fmt.Errorf("failed to get app status: %w", err)
	}

	if output == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(statusResp)
	}

	m := newStatusModel(appName, statusResp)
	fmt.Println(m.View())
	return nil
}

func init() {
	statusCmd.Flags().StringP("app", "a", "", "Application name")
	statusCmd.Flags().String("org", "", "organization ID")
	statusCmd.Flags().String("workspace", "", "workspace ID")
	statusCmd.Flags().StringP("output", "", "table", "Output format: table | json")
	statusCmd.Flags().String("host", "", "Set the host URL")
}

type statusModel struct {
	response *resourcev1.GetResourceStatusResponse
	appName  string
}

func newStatusModel(appName string, resp *resourcev1.GetResourceStatusResponse) statusModel {
	return statusModel{response: resp, appName: appName}
}

func (m statusModel) View() string {
	titleStyle := lipgloss.NewStyle().
		Foreground(ui.LocoCyan).
		Bold(true).
		MarginBottom(1)

	labelStyle := lipgloss.NewStyle().
		Foreground(ui.LocoDimGrey).
		Width(18)

	valueStyle := lipgloss.NewStyle().
		Foreground(ui.LocoWhite).
		Bold(true)

	blockStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.LocoOrange).
		Padding(1, 2).
		Margin(1, 2)

	var status, replicas string

	status = m.response.CurrentDeployment.Status.String()
	replicas = fmt.Sprintf("%d", m.response.CurrentDeployment.Replicas)

	url := "hostname management pending"

	content := fmt.Sprintf(
		"%s %s\n%s %s\n%s %s\n%s %s",
		labelStyle.Render("App:"), valueStyle.Render(m.appName),
		labelStyle.Render("Status:"), valueStyle.Render(status),
		labelStyle.Render("Replicas:"), valueStyle.Render(replicas),
		labelStyle.Render("URL:"), valueStyle.Render(url),
	)

	return titleStyle.Render("Application Status") + "\n" + blockStyle.Render(content)
}
