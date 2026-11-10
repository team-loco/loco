package resource

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/ui"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

const noEventsMessage = "No events found."

var errInvalidEventLimit = errors.New("limit must be nonnegative")

var eventsTableMargin = lipgloss.NewStyle().Margin(1, 2)

func newEventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events <name>",
		Short: "Show service events",
		Long: `Display Loco events for a service.

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

			t, err := cmdutil.ResolveTarget(ctx, cmd)
			if err != nil {
				return err
			}
			resource, err := t.Service(ctx, name)
			if err != nil {
				return err
			}
			slog.Debug("fetching events", "resource_id", resource.GetId(), "name", name)

			workspace, err := t.Clients.Workspace.GetWorkspace(
				ctx,
				&workspacev1.GetWorkspaceRequest{WorkspaceId: t.WorkspaceID},
			)
			if err != nil {
				return fmt.Errorf("get workspace: %w", err)
			}
			request := &eventv1.ListEventsRequest{
				OrgId:       workspace.GetWorkspace().GetOrgId(),
				WorkspaceId: t.WorkspaceID,
				ResourceId:  resource.GetId(),
				PageSize:    500,
			}
			if limit < 0 {
				return errInvalidEventLimit
			}
			var items []*eventv1.Event
			for {
				if limit > 0 {
					request.PageSize = min(500, limit-int32(len(items)))
				}
				response, err := t.Clients.Event.ListEvents(ctx, request)
				if err != nil {
					return fmt.Errorf("failed to fetch events: %w", err)
				}
				items = append(items, response.GetEvents()...)
				if response.GetNextBeforeSeq() == 0 || (limit > 0 && len(items) >= int(limit)) {
					break
				}
				request.BeforeSeq = response.GetNextBeforeSeq()
			}

			if output == outputJSON {
				return writeEventsJSON(cmd.OutOrStdout(), items)
			}

			return renderEventsTable(cmd.OutOrStdout(), items)
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().String("output", "table", "Output format (table, json)")
	cmd.Flags().Int32("limit", 0, "Maximum number of events to display (0 = all)")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}

func writeEventsJSON(stdout io.Writer, events []*eventv1.Event) error {
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

func renderEventsTable(stdout io.Writer, events []*eventv1.Event) error {
	if len(events) == 0 {
		_, err := fmt.Fprintln(stdout, noEventsMessage)
		return err
	}

	rows := make([][]string, 0, len(events))
	for _, event := range events {
		rows = append(rows, []string{
			event.GetCreatedAt().AsTime().Format(time.RFC3339),
			event.GetType(),
			event.GetData().GetFields()["message"].GetStringValue(),
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
		Headers("TIME", "TYPE", "MESSAGE").
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
