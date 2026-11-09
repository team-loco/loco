package resource

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/ui"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const noEventsMessage = "No events found."

var eventsTableMargin = lipgloss.NewStyle().Margin(1, 2)

func newEventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events <name>",
		Short: "Show service events",
		Long: `Display Kubernetes events for a service's deployment.

Examples:
  loco resource events myapp
  loco resource events myapp --limit 20 --since 6h
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

			since, err := cmd.Flags().GetDuration("since")
			if err != nil {
				return fmt.Errorf("error reading since flag: %w", err)
			}

			t, err := cmdutil.ResolveTarget(ctx, cmd)
			if err != nil {
				return err
			}
			resource, err := t.Service(ctx, name)
			if err != nil {
				return err
			}
			slog.Debug("fetching events", "resource_id", resource.GetId(), "name", name)

			end := time.Now()
			eventsReq := &observabilityv1.QueryEventsRequest{
				WorkspaceId: t.WorkspaceID,
				ResourceId:  resource.GetId(),
				StartTime:   timestamppb.New(end.Add(-since)),
				EndTime:     timestamppb.New(end),
				Limit:       limit,
			}

			resp, err := t.Clients.ObservabilityProxy.QueryEvents(ctx, eventsReq)
			if err != nil {
				return fmt.Errorf("failed to fetch events: %w", err)
			}

			if output == outputJSON {
				return writeEventsJSON(cmd.OutOrStdout(), resp.GetEvents())
			}

			return renderEventsTable(cmd.OutOrStdout(), resp.GetEvents())
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().String("output", "table", "Output format (table, json)")
	cmd.Flags().Int32("limit", 0, "Maximum number of events to display (0 = the proxy's default)")
	cmd.Flags().Duration("since", defaultLogWindow, "How far back to look, up to 24h")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}

func writeEventsJSON(stdout io.Writer, events []*observabilityv1.Event) error {
	items := make([]json.RawMessage, 0, len(events))
	for _, event := range events {
		item, err := protojson.Marshal(event)
		if err != nil {
			return fmt.Errorf("encode event: %w", err)
		}
		items = append(items, item)
	}
	out, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("encode events: %w", err)
	}
	_, err = fmt.Fprintln(stdout, string(out))
	return err
}

func renderEventsTable(stdout io.Writer, events []*observabilityv1.Event) error {
	if len(events) == 0 {
		_, err := fmt.Fprintln(stdout, noEventsMessage)
		return err
	}

	rows := make([][]string, 0, len(events))
	for _, event := range events {
		rows = append(rows, []string{
			event.GetTimestamp().AsTime().Format(time.RFC3339),
			event.GetReason(),
			simplifyMessage(event.GetMessage()),
		})
	}
	cellStyle := lipgloss.NewStyle().Padding(0, 1)
	headerStyle := cellStyle.Foreground(ui.Fg3)
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(ui.Line2)).
		BorderTop(false).
		BorderBottom(false).
		BorderLeft(false).
		BorderRight(false).
		BorderColumn(false).
		Headers("TIME", "REASON", "MESSAGE").
		Rows(rows...).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerStyle
			}
			return cellStyle
		})
	_, err := lipgloss.Fprintln(stdout, eventsTableMargin.Render(t.String()))
	return err
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
