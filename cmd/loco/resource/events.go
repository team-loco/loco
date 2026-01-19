package resource

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/ui"
	"github.com/team-loco/loco/shared"
	resourcev1 "github.com/team-loco/loco/shared/proto/loco/resource/v1"
	"github.com/team-loco/loco/shared/proto/loco/resource/v1/resourcev1connect"
)

type eventsDeps struct {
	LoadSessionConfig func() (*config.SessionConfig, error)
	NewAPIClient      func(host, token string) *client.Client
	NewResourceClient func(host string) resourcev1connect.ResourceServiceClient
	Stdout            io.Writer
}

func buildEventsCmd() *cobra.Command {
	deps := eventsDeps{
		LoadSessionConfig: config.Load,
		NewAPIClient:      client.NewClient,
		NewResourceClient: func(host string) resourcev1connect.ResourceServiceClient {
			return resourcev1connect.NewResourceServiceClient(shared.NewHTTPClient(), host)
		},
		Stdout: os.Stdout,
	}
	return newEventsCmd(deps)
}

func newEventsCmd(deps eventsDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events <name>",
		Short: "Show service events",
		Long: `Display Kubernetes events for a service's deployment.

Examples:
  loco service events myapp
  loco service events myapp --limit 20
  loco service events myapp --output json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]

			output, err := cmd.Flags().GetString("output")
			if err != nil {
				return fmt.Errorf("error reading output flag: %w", err)
			}

			limit, err := cmd.Flags().GetInt32("limit")
			if err != nil {
				return fmt.Errorf("error reading limit flag: %w", err)
			}

			// Get host and token
			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}

			locoToken, err := cmdutil.GetCurrentLocoToken()
			if err != nil {
				return fmt.Errorf("login required - please run 'loco login'")
			}

			// Resolve workspace ID
			apiClient := deps.NewAPIClient(host, locoToken.Token)
			workspaceID, err := resolveWorkspaceID(ctx, cmd, deps.LoadSessionConfig, apiClient)
			if err != nil {
				return err
			}

			// Create resource client
			resourceClient := deps.NewResourceClient(host)
			authHeader := fmt.Sprintf("Bearer %s", locoToken.Token)

			// Get resource by name
			getReq := connect.NewRequest(&resourcev1.GetResourceRequest{
				Key: &resourcev1.GetResourceRequest_NameKey{
					NameKey: &resourcev1.GetResourceNameKey{
						WorkspaceId: workspaceID,
						Name:        name,
					},
				},
			})
			getReq.Header().Set("Authorization", authHeader)

			resourceResp, err := resourceClient.GetResource(ctx, getReq)
			if err != nil {
				return fmt.Errorf("service '%s' not found: %w", name, err)
			}

			resource := resourceResp.Msg.Resource
			slog.Debug("fetching events", "resource_id", resource.Id, "name", name)

			var limitPtr *int32
			if limit > 0 {
				limitPtr = &limit
			}

			eventsReq := connect.NewRequest(&resourcev1.ListResourceEventsRequest{
				ResourceId: resource.Id,
				Limit:      limitPtr,
			})
			eventsReq.Header().Set("Authorization", authHeader)

			resp, err := resourceClient.ListResourceEvents(ctx, eventsReq)
			if err != nil {
				slog.Error("failed to fetch events", "error", err)
				return fmt.Errorf("failed to fetch events: %w", err)
			}

			if output == "json" {
				encoder := json.NewEncoder(deps.Stdout)
				encoder.SetIndent("", "  ")
				return encoder.Encode(resp.Msg.Events)
			}

			renderEventsTable(deps.Stdout, resp.Msg.Events)
			return nil
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().String("output", "table", "Output format (table, json)")
	cmd.Flags().Int32("limit", 0, "Maximum number of events to display (0 = all)")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}

func renderEventsTable(stdout io.Writer, events []*resourcev1.Event) {
	if len(events) == 0 {
		fmt.Fprintln(stdout, "No events found.")
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
	fmt.Fprintln(stdout, tableStyle.Render(t.View()))
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
