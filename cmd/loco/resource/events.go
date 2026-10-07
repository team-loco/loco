package resource

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/infra"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/ui"
)

type eventsDeps struct {
	NewResourceClient func(host string) resourcev1connect.ResourceServiceClient
	Stdout            io.Writer
}

func buildEventsCmd() *cobra.Command {
	deps := eventsDeps{
		NewResourceClient: func(host string) resourcev1connect.ResourceServiceClient {
			return resourcev1connect.NewResourceServiceClient(httputil.NewHTTPClient(), host)
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
  loco resource events myapp
  loco resource events myapp --limit 20
  loco resource events myapp --output json`,
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

			if output == outputJSON {
				encoder := json.NewEncoder(deps.Stdout)
				encoder.SetIndent("", "  ")
				return encoder.Encode(resp.Msg.Events)
			}

			renderEventsTable(deps.Stdout, resp.Msg.Events)
			return nil
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name or ID")
	cmd.Flags().String("environment", "", "Environment name or ID")
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
			BorderForeground(ui.Line2).
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
