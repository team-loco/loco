package resource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/logstream"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	outputTable        = "table"
	noEventsMessage    = "No events found."
	defaultEventLimit  = 50
	defaultEventWindow = logstream.MaxRange
)

var (
	errEventLimitNotPositive  = errors.New("--limit must be at least 1")
	errEventsSinceTooLong     = errors.New("the observability proxy reads at most 24h back; lower --since")
	errEventsSinceNotPositive = errors.New("--since must be positive")

	eventsTableMargin = lipgloss.NewStyle().Margin(1, 2)
)

type eventsDeps struct {
	LoadSessionConfig func() (*session.SessionConfig, error)
	NewAPIClient      func(host, token string) *client.Client
	Clients           platformClients
	Stdout            io.Writer
}

func buildEventsCmd() *cobra.Command {
	deps := eventsDeps{
		LoadSessionConfig: session.Load,
		NewAPIClient:      client.NewClient,
		Clients:           defaultPlatformClients(),
		Stdout:            os.Stdout,
	}
	return newEventsCmd(deps)
}

func newEventsCmd(deps eventsDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events <name>",
		Short: "Show service events",
		Long: `Show the Kubernetes events of a service's pods, ReplicaSets and Deployments in one
environment, newest first, from every region it runs in.

--env or LOCO_ENV names the environment; a workspace with a single environment uses it.

Examples:
  loco resource events myapp
  loco resource events myapp --env staging --since 6h
  loco resource events myapp --limit 20 --output json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResourceEvents(cmd, deps, args[0])
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().String("env", "", "Environment name (defaults to the workspace's only environment)")
	cmd.Flags().Duration("since", defaultEventWindow, "How far back to look, up to 24h")
	cmd.Flags().Int32("limit", defaultEventLimit, "Maximum number of events to show")
	cmd.Flags().StringP("output", "o", outputTable, "Output format (table, json)")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}

type eventOptions struct {
	since  time.Duration
	limit  int32
	output string
}

func readEventOptions(cmd *cobra.Command) (eventOptions, error) {
	var opts eventOptions
	var err error
	if opts.since, err = cmd.Flags().GetDuration("since"); err != nil {
		return opts, fmt.Errorf("error reading since flag: %w", err)
	}
	if opts.limit, err = cmd.Flags().GetInt32("limit"); err != nil {
		return opts, fmt.Errorf("error reading limit flag: %w", err)
	}
	if opts.output, err = cmd.Flags().GetString("output"); err != nil {
		return opts, fmt.Errorf("error reading output flag: %w", err)
	}
	if opts.since <= 0 {
		return opts, errEventsSinceNotPositive
	}
	if opts.since > logstream.MaxRange {
		return opts, errEventsSinceTooLong
	}
	if opts.limit < 1 {
		return opts, errEventLimitNotPositive
	}
	if opts.output != outputTable && opts.output != outputJSON {
		return opts, fmt.Errorf("invalid output format %q: use table or json", opts.output)
	}
	return opts, nil
}

func runResourceEvents(cmd *cobra.Command, deps eventsDeps, name string) error {
	opts, err := readEventOptions(cmd)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	host, err := cmdutil.GetHost(cmd)
	if err != nil {
		return err
	}
	locoToken, err := cmdutil.GetCurrentLocoToken(cmd)
	if err != nil {
		return err
	}

	apiClient := deps.NewAPIClient(host, locoToken.Token)
	workspaceID, err := cmdutil.ResolveWorkspaceID(ctx, cmd, deps.LoadSessionConfig, apiClient)
	if err != nil {
		return err
	}
	environments := deps.Clients.Environments(host, locoToken.Token)
	env, err := cmdutil.ResolveEnvironment(ctx, cmd, environments, nil, false, workspaceID)
	if err != nil {
		return err
	}
	resourceClient := deps.Clients.Resources(host, locoToken.Token)
	resource, err := getResourceByName(ctx, resourceClient, workspaceID, name)
	if err != nil {
		return err
	}

	access := deps.Clients.Access(host, locoToken.Token)
	proxies, err := logstream.Proxies(ctx, access, workspaceID, "")
	if errors.Is(err, logstream.ErrNoProxy) {
		return fmt.Errorf("no events to show for %s: it has no running deployment with an observability proxy", name)
	}
	if err != nil {
		return err
	}

	now := time.Now()
	since := now.Add(-opts.since)
	startTime := timestamppb.New(since)
	endTime := timestamppb.New(now)
	req := &observabilityv1.QueryEventsRequest{
		WorkspaceId:   workspaceID,
		EnvironmentId: env.GetId(),
		ResourceIds:   []string{resource.GetId()},
		StartTime:     startTime,
		EndTime:       endTime,
		Limit:         opts.limit,
	}
	events, err := queryEvents(ctx, deps.Clients, locoToken.Token, proxies, req)
	if err != nil {
		return err
	}
	if opts.output == outputJSON {
		return writeEventsJSON(deps.Stdout, events)
	}
	return renderEventsTable(deps.Stdout, events)
}

func queryEvents(
	ctx context.Context,
	clients platformClients,
	token string,
	proxies []logstream.Proxy,
	msg *observabilityv1.QueryEventsRequest,
) ([]*observabilityv1.KubernetesEvent, error) {
	var events []*observabilityv1.KubernetesEvent
	for _, proxy := range proxies {
		proxyClient := clients.Proxy(proxy.URL, token)
		resp, err := proxyClient.QueryEvents(ctx, msg)
		if err != nil {
			return nil, fmt.Errorf("query events in %s: %w", proxy.Region, err)
		}
		events = append(events, resp.GetEvents()...)
	}
	slices.SortStableFunc(events, func(a, b *observabilityv1.KubernetesEvent) int {
		newer := b.GetTimestamp().AsTime()
		older := a.GetTimestamp().AsTime()
		return newer.Compare(older)
	})
	limit := int(msg.GetLimit())
	if len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}

func writeEventsJSON(stdout io.Writer, events []*observabilityv1.KubernetesEvent) error {
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

func renderEventsTable(stdout io.Writer, events []*observabilityv1.KubernetesEvent) error {
	if len(events) == 0 {
		_, err := fmt.Fprintln(stdout, noEventsMessage)
		return err
	}

	rows := make([][]string, 0, len(events))
	for _, event := range events {
		object := event.GetObjectKind() + "/" + event.GetObjectName()
		count := strconv.FormatInt(event.GetCount(), 10)
		rows = append(rows, []string{
			event.GetTimestamp().AsTime().Format(time.RFC3339),
			event.GetType(),
			event.GetReason(),
			object,
			count,
			event.GetMessage(),
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
		Headers("TIME", "TYPE", "REASON", "OBJECT", "COUNT", "MESSAGE").
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
