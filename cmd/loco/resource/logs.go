package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"connectrpc.com/connect"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
	"github.com/team-loco/loco/internal/httputil"
	resourcev1 "github.com/team-loco/loco/proto/loco/resource/v1"
	"github.com/team-loco/loco/proto/loco/resource/v1/resourcev1connect"
)

type logsDeps struct {
	LoadSessionConfig func() (*session.SessionConfig, error)
	NewAPIClient      func(host, token string) *client.Client
	NewResourceClient func(host string) resourcev1connect.ResourceServiceClient
	Stdout            io.Writer
}

func buildLogsCmd() *cobra.Command {
	deps := logsDeps{
		LoadSessionConfig: session.Load,
		NewAPIClient:      client.NewClient,
		NewResourceClient: func(host string) resourcev1connect.ResourceServiceClient {
			return resourcev1connect.NewResourceServiceClient(httputil.NewHTTPClient(), host)
		},
		Stdout: os.Stdout,
	}
	return newLogsCmd(deps)
}

func newLogsCmd(deps logsDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "View service logs",
		Long: `Stream logs from a service's running deployment.

Examples:
  loco service logs myapp
  loco service logs myapp --follow
  loco service logs myapp --lines 100 --output json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			output, err := cmd.Flags().GetString("output")
			if err != nil {
				return fmt.Errorf("error reading output flag: %w", err)
			}

			switch output {
			case "json":
				return streamLogsJSON(cmd, deps, name)
			case "table", "":
				return streamLogsInteractive(cmd, deps, name)
			default:
				return fmt.Errorf("invalid output format: %s", output)
			}
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().BoolP("follow", "f", false, "Follow log output (tail -f style)")
	cmd.Flags().Int32P("lines", "n", 0, "Number of lines to show (0 = all)")
	cmd.Flags().StringP("output", "o", "", "Output format (json, table). Defaults to table.")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}

func streamLogsJSON(cmd *cobra.Command, deps logsDeps, name string) error {
	ctx := cmd.Context()

	lines, err := cmd.Flags().GetInt32("lines")
	if err != nil {
		return fmt.Errorf("error reading lines flag: %w", err)
	}

	follow, err := cmd.Flags().GetBool("follow")
	if err != nil {
		return fmt.Errorf("error reading follow flag: %w", err)
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
	slog.Debug("streaming logs as json", "resource_id", resource.Id, "name", name)

	var linesPtr *int32
	if lines > 0 {
		linesPtr = &lines
	}

	var followPtr *bool
	if follow {
		followPtr = &follow
	}

	logsReq := connect.NewRequest(&resourcev1.WatchLogsRequest{
		ResourceId: resource.Id,
		Limit:      linesPtr,
		Follow:     followPtr,
	})
	logsReq.Header().Set("Authorization", authHeader)

	stream, err := resourceClient.WatchLogs(ctx, logsReq)
	if err != nil {
		return fmt.Errorf("failed to stream logs: %w", err)
	}

	for stream.Receive() {
		logEntry := stream.Msg()
		jsonLog, marshalErr := json.Marshal(logEntry)
		if marshalErr != nil {
			slog.Debug("failed to marshal log entry", "error", marshalErr)
			fmt.Fprintf(deps.Stdout, "Error marshaling log: %v\n", marshalErr)
			continue
		}
		fmt.Fprintln(deps.Stdout, string(jsonLog))
	}

	if err := stream.Err(); err != nil {
		return fmt.Errorf("log stream error: %w", err)
	}

	return nil
}

func streamLogsInteractive(cmd *cobra.Command, deps logsDeps, name string) error {
	ctx := cmd.Context()

	lines, err := cmd.Flags().GetInt32("lines")
	if err != nil {
		return fmt.Errorf("error reading lines flag: %w", err)
	}

	follow, err := cmd.Flags().GetBool("follow")
	if err != nil {
		return fmt.Errorf("error reading follow flag: %w", err)
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
	slog.Debug("streaming logs interactively", "resource_id", resource.Id, "name", name)

	columns := []table.Column{
		{Title: "Time", Width: 20},
		{Title: "Pod", Width: 30},
		{Title: "Message", Width: 80},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows([]table.Row{}),
		table.WithFocused(true),
		table.WithHeight(20),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(ui.LocoMuted).
		BorderBottom(true).
		Bold(false)
	s.Selected = s.Selected.
		Foreground(ui.LocoWhite).
		Background(ui.LocoGreen).
		Bold(false)
	t.SetStyles(s)

	logsChan := make(chan *resourcev1.WatchLogsResponse)
	errChan := make(chan error)

	var linesPtr *int32
	if lines > 0 {
		linesPtr = &lines
	}

	var followPtr *bool
	if follow {
		followPtr = &follow
	}

	go func() {
		logsReq := connect.NewRequest(&resourcev1.WatchLogsRequest{
			ResourceId: resource.Id,
			Limit:      linesPtr,
			Follow:     followPtr,
		})
		logsReq.Header().Set("Authorization", authHeader)

		stream, streamErr := resourceClient.WatchLogs(ctx, logsReq)
		if streamErr != nil {
			errChan <- streamErr
			return
		}

		for stream.Receive() {
			logsChan <- stream.Msg()
		}

		if streamErr := stream.Err(); streamErr != nil {
			errChan <- streamErr
		}
	}()

	m := logModel{
		table:     t,
		baseStyle: lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(ui.LocoGreyish),
		logs:      []table.Row{},
		logsChan:  logsChan,
		errChan:   errChan,
		ctx:       ctx,
	}

	if finalModel, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Fprintf(deps.Stdout, "Error running log viewer: %v\n", err)
		return err
	} else if fm, ok := finalModel.(logModel); ok && fm.err != nil {
		return fm.err
	}

	return nil
}

// Log model for bubbletea
type logMsg struct {
	Time    string
	PodName string
	Message string
}

type errMsg struct{ error }

type logModel struct {
	table     table.Model
	baseStyle lipgloss.Style
	logsChan  chan *resourcev1.WatchLogsResponse
	errChan   chan error
	logs      []table.Row
	ctx       context.Context
	err       error
}

func (m logModel) Init() tea.Cmd {
	return m.waitForLog()
}

func (m logModel) waitForLog() tea.Cmd {
	return func() tea.Msg {
		select {
		case log := <-m.logsChan:
			return logMsg{
				Time:    log.Timestamp.AsTime().Format(time.RFC3339),
				PodName: log.PodName,
				Message: log.Log,
			}
		case err := <-m.errChan:
			return errMsg{err}
		case <-m.ctx.Done():
			return tea.Quit()
		case <-time.After(100 * time.Millisecond):
			return m.waitForLog()
		}
	}
}

func (m logModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case logMsg:
		newRow := table.Row{msg.Time, msg.PodName, msg.Message}
		m.logs = append(m.logs, newRow)
		m.table.SetRows(m.logs)
		return m, m.waitForLog()

	case errMsg:
		m.err = msg.error
		return m, tea.Quit

	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			if m.table.Focused() {
				m.table.Blur()
			} else {
				m.table.Focus()
			}
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}

	m.table, cmd = m.table.Update(msg)
	return m, tea.Batch(cmd, m.waitForLog())
}

func (m logModel) View() tea.View {
	var content string
	if m.err != nil {
		content = lipgloss.NewStyle().Foreground(ui.LocoRed).Render(
			fmt.Sprintf("Error: %v", m.err),
		)
	} else {
		content = m.baseStyle.Render(m.table.View()) +
			"\n[↑↓] Navigate • [esc] Toggle focus • [q] Quit"
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}
