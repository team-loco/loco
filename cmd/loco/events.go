package loco

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/ui"
	appv1 "github.com/team-loco/loco/shared/proto/app/v1"
)

func init() {
	eventsCmd.Flags().StringP("app", "a", "", "Application name")
	eventsCmd.Flags().String("org", "", "organization ID")
	eventsCmd.Flags().String("workspace", "", "workspace ID")
	eventsCmd.Flags().String("output", "table", "Output format (table, json). Defaults to table.")
	eventsCmd.Flags().Int32("limit", 0, "Maximum number of events to display (0 = all)")
	eventsCmd.Flags().String("host", "", "Set the host URL")
}

var eventsCmd = &cobra.Command{
	Use:   "events",
	Short: "Show application events",
	Long:  "Display Kubernetes events for an application's deployment.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return eventsCmdFunc(cmd)
	},
}

func eventsCmdFunc(cmd *cobra.Command) error {
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

	limit, err := cmd.Flags().GetInt32("limit")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFlagParsing, err)
	}

	locoToken, err := getLocoToken()
	if err != nil {
		return ErrLoginRequired
	}

	apiClient := client.NewClient(host, locoToken.Token)

	slog.Debug("fetching app by name", "workspaceId", workspaceID, "app_name", appName)

	app, err := apiClient.GetAppByName(ctx, workspaceID, appName)
	if err != nil {
		slog.Debug("failed to get app by name", "error", err)
		return fmt.Errorf("failed to get app '%s': %w", appName, err)
	}

	appID := app.Id
	slog.Debug("found app by name", "app_name", appName, "app_id", appID)

	slog.Debug("fetching events for app", "app_id", appID, "app_name", appName)

	var limitPtr *int32
	if limit > 0 {
		limitPtr = &limit
	}

	events, err := apiClient.GetEvents(ctx, appID, limitPtr)
	if err != nil {
		slog.Error("failed to fetch events", "error", err)
		return fmt.Errorf("failed to fetch events: %w", err)
	}

	if output == "json" {
		return printEventsJSON(events)
	}

	printEventsTable(events)
	return nil
}

func printEventsTable(events []*appv1.Event) {
	if len(events) == 0 {
		fmt.Println("No events found.")
		return
	}

	columns := []table.Column{
		{Title: "TIME", Width: 20},
		{Title: "REASON", Width: 20},
		{Title: "MESSAGE", Width: 80},
	}

	var rows []table.Row
	for _, event := range events {
		rows = append(rows, table.Row{
			event.Timestamp.AsTime().Format(time.RFC3339),
			event.Reason,
			simplifyMessage(event.Message),
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithHeight(len(rows)),
	)

	s := table.Styles{
		Header: lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(ui.LocoMuted).
			BorderBottom(true).
			Bold(false),
		Cell: lipgloss.NewStyle().Padding(0, 1),
	}
	t.SetStyles(s)

	tableStyle := lipgloss.NewStyle().Margin(1, 2)
	fmt.Println(tableStyle.Render(t.View()))
}

func simplifyMessage(message string) string {
	if strings.Contains(message, "ImagePullBackOff") {
		return "Error: ImagePullBackOff"
	}
	if strings.Contains(message, "Failed to pull image") {
		return "Failed to pull image. Please check registry credentials and image path."
	}
	return message
}

func printEventsJSON(events []*appv1.Event) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(events)
}
