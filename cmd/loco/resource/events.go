package resource

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/team-loco/loco/internal/rpc"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
	"google.golang.org/protobuf/encoding/protojson"
)

const noEventsMessage = "No events found."

var eventsTableMargin = lipgloss.NewStyle().Margin(1, 2)

type eventsDeps struct {
	LoadSessionConfig func() (*session.SessionConfig, error)
	NewAPIClient      func(host, token string) *client.Client
	NewResourceClient func(host, token string) resourcev1connect.ResourceServiceClient
	Stdout            io.Writer
}

func buildEventsCmd() *cobra.Command {
	deps := eventsDeps{
		LoadSessionConfig: session.Load,
		NewAPIClient:      client.NewClient,
		NewResourceClient: func(host, token string) resourcev1connect.ResourceServiceClient {
			resourceClients := rpc.ForHost(host, token)
			return resourceClients.Resource
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

			// Get host and token
			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}

			locoToken, err := cmdutil.GetCurrentLocoToken(cmd)
			if err != nil {
				return err
			}

			// Resolve workspace ID
			apiClient := deps.NewAPIClient(host, locoToken.Token)
			workspaceID, err := cmdutil.ResolveWorkspaceID(ctx, cmd, deps.LoadSessionConfig, apiClient)
			if err != nil {
				return err
			}

			// Create resource client
			resourceClient := deps.NewResourceClient(host, locoToken.Token)

			// Get resource by name
			getReq := connect.NewRequest(&resourcev1.GetResourceRequest{
				Key: &resourcev1.GetResourceRequest_NameKey{
					NameKey: &resourcev1.GetResourceNameKey{
						WorkspaceId: workspaceID,
						Name:        name,
					},
				},
			})

			resourceResp, err := resourceClient.GetResource(ctx, getReq)
			if err != nil {
				return fmt.Errorf("service '%s' not found: %w", name, err)
			}

			resource := resourceResp.Msg.GetResource()
			slog.Debug("fetching events", "resource_id", resource.GetId(), "name", name)

			var limitPtr *int32
			if limit > 0 {
				limitPtr = &limit
			}

			eventsReq := connect.NewRequest(&resourcev1.ListResourceEventsRequest{
				ResourceId: resource.GetId(),
				Limit:      limitPtr,
			})

			resp, err := resourceClient.ListResourceEvents(ctx, eventsReq)
			if err != nil {
				slog.Error("failed to fetch events", "error", err)
				return fmt.Errorf("failed to fetch events: %w", err)
			}

			if output == outputJSON {
				return writeEventsJSON(deps.Stdout, resp.Msg.GetEvents())
			}

			return renderEventsTable(deps.Stdout, resp.Msg.GetEvents())
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().String("output", "table", "Output format (table, json)")
	cmd.Flags().Int32("limit", 0, "Maximum number of events to display (0 = all)")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}

func writeEventsJSON(stdout io.Writer, events []*resourcev1.Event) error {
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

func renderEventsTable(stdout io.Writer, events []*resourcev1.Event) error {
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
