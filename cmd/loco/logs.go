package loco

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/ui"
	resourcev1 "github.com/team-loco/loco/shared/proto/loco/resource/v1"
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "View application logs",
	Long:  "Stream logs from an application's running deployment.",
	RunE: func(cmd *cobra.Command, args []string) error {
		output, err := cmd.Flags().GetString("output")
		if err != nil {
			return fmt.Errorf("%w: %w", ErrFlagParsing, err)
		}

		switch output {
		case "json":
			return streamLogsAsJson(cmd)
		case "table":
			return streamLogsInteractive(cmd)
		case "": // default
			return streamLogsInteractive(cmd)
		default:
			return fmt.Errorf("invalid output format: %s", output)
		}
	},
}

func streamLogsAsJson(cmd *cobra.Command) error {
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

	lines, err := cmd.Flags().GetInt32("lines")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFlagParsing, err)
	}

	follow, err := cmd.Flags().GetBool("follow")
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

	slog.Debug("streaming logs as json", "app_id", appID, "app_name", appName)

	var linesPtr *int32
	if lines > 0 {
		linesPtr = &lines
	}

	var followPtr *bool
	if follow {
		followPtr = &follow
	}

	err = apiClient.StreamLogs(ctx, appID, linesPtr, followPtr, func(logEntry *resourcev1.WatchLogsResponse) error {
		jsonLog, marshalErr := json.Marshal(logEntry)
		if marshalErr != nil {
			slog.Debug("failed to marshal log entry to json", "error", marshalErr)
			fmt.Fprintf(os.Stderr, "Error marshaling log: %v\n", marshalErr)
			return nil
		}
		fmt.Println(string(jsonLog))
		return nil
	})
	if err != nil {
		slog.Error("failed to stream logs", "error", err)
		return fmt.Errorf("failed to stream logs: %w", err)
	}

	return nil
}

func streamLogsInteractive(cmd *cobra.Command) error {
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

	lines, err := cmd.Flags().GetInt32("lines")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFlagParsing, err)
	}

	follow, err := cmd.Flags().GetBool("follow")
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
		err := apiClient.StreamLogs(ctx, appID, linesPtr, followPtr, func(logEntry *resourcev1.WatchLogsResponse) error {
			logsChan <- logEntry
			return nil
		})
		if err != nil {
			errChan <- err
		}
	}()

	slog.Debug("streaming logs for app", "app_name", appName, "app_id", appID)

	m := logModel{
		table:     t,
		baseStyle: lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(ui.LocoGreyish),
		logs:      []table.Row{},
		logsChan:  logsChan,
		errChan:   errChan,
		ctx:       ctx,
	}

	if finalModel, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running log viewer: %v\n", err)
		return err
	} else if fm, ok := finalModel.(logModel); ok && fm.err != nil {
		slog.Debug("log streaming failed", "error", fm.err)
		return fm.err
	}

	return nil
}

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

	case tea.KeyMsg:
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

func (m logModel) View() string {
	if m.err != nil {
		return lipgloss.NewStyle().Foreground(ui.LocoRed).Render(
			fmt.Sprintf("Error: %v", m.err),
		)
	}
	return m.baseStyle.Render(m.table.View()) +
		"\n[↑↓] Navigate • [esc] Toggle focus • [q] Quit"
}

func init() {
	logsCmd.Flags().StringP("app", "a", "", "Application name")
	logsCmd.Flags().String("org", "", "organization ID")
	logsCmd.Flags().String("workspace", "", "workspace ID")
	logsCmd.Flags().BoolP("follow", "f", false, "Follow log output (tail -f style)")
	logsCmd.Flags().Int32P("lines", "n", 0, "Number of lines to show (0 = all)")
	logsCmd.Flags().StringP("output", "o", "", "Output format (json, table). Defaults to table.")
	logsCmd.Flags().String("host", "", "Set the host URL")
}
