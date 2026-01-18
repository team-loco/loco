package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/service/internal"
	"github.com/team-loco/loco/internal/ui"
	resourcev1 "github.com/team-loco/loco/shared/proto/loco/resource/v1"
)

func buildEventsCmd(deps *internal.ServiceDeps) *cobra.Command {
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
			name := args[0]
			runner := &eventsRunner{
				deps: deps,
				name: name,
			}
			return runner.Run(cmd)
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().String("output", "table", "Output format (table, json)")
	cmd.Flags().Int32("limit", 0, "Maximum number of events to display (0 = all)")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}

type eventsRunner struct {
	deps *internal.ServiceDeps
	name string
}

func (r *eventsRunner) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	output, err := cmd.Flags().GetString("output")
	if err != nil {
		return fmt.Errorf("error reading output flag: %w", err)
	}

	limit, err := cmd.Flags().GetInt32("limit")
	if err != nil {
		return fmt.Errorf("error reading limit flag: %w", err)
	}

	// Resolve resource
	resolver := internal.NewContextResolver(r.deps)
	resource, err := resolver.ResolveResourceByName(ctx, cmd, r.name)
	if err != nil {
		return err
	}

	slog.Debug("fetching events", "resource_id", resource.Id, "name", r.name)

	var limitPtr *int32
	if limit > 0 {
		limitPtr = &limit
	}

	req := connect.NewRequest(&resourcev1.ListResourceEventsRequest{
		ResourceId: resource.Id,
		Limit:      limitPtr,
	})
	req.Header().Set("Authorization", r.deps.AuthHeader())

	resp, err := r.deps.ListResourceEvents(ctx, req)
	if err != nil {
		slog.Error("failed to fetch events", "error", err)
		return fmt.Errorf("failed to fetch events: %w", err)
	}

	if output == "json" {
		encoder := json.NewEncoder(r.deps.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(resp.Msg.Events)
	}

	r.renderEventsTable(resp.Msg.Events)
	return nil
}

func (r *eventsRunner) renderEventsTable(events []*resourcev1.Event) {
	if len(events) == 0 {
		fmt.Fprintln(r.deps.Stdout, "No events found.")
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
	fmt.Fprintln(r.deps.Stdout, tableStyle.Render(t.View()))
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
