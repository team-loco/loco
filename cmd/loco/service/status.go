package service

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/service/internal"
	"github.com/team-loco/loco/internal/ui"
	resourcev1 "github.com/team-loco/loco/shared/proto/resource/v1"
)

func buildStatusCmd(deps *internal.ServiceDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show service status",
		Long: `Display the current status of a service.

Examples:
  loco service status myapp
  loco service status myapp --output json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			runner := &statusRunner{
				deps: deps,
				name: name,
			}
			return runner.Run(cmd)
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().StringP("output", "o", "table", "Output format: table | json")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}

type statusRunner struct {
	deps *internal.ServiceDeps
	name string
}

func (r *statusRunner) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	output, err := cmd.Flags().GetString("output")
	if err != nil {
		return fmt.Errorf("error reading output flag: %w", err)
	}

	// Resolve resource
	resolver := internal.NewContextResolver(r.deps)
	resource, err := resolver.ResolveResourceByName(ctx, cmd, r.name)
	if err != nil {
		return err
	}

	slog.Debug("fetching service status", "resource_id", resource.Id, "name", r.name)

	req := connect.NewRequest(&resourcev1.GetResourceStatusRequest{
		ResourceId: resource.Id,
	})
	req.Header().Set("Authorization", r.deps.AuthHeader())

	resp, err := r.deps.GetResourceStatus(ctx, req)
	if err != nil {
		slog.Error("failed to get service status", "error", err)
		return fmt.Errorf("failed to get service status: %w", err)
	}

	if output == "json" {
		encoder := json.NewEncoder(r.deps.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(resp.Msg)
	}

	// Render table view
	r.renderStatusView(resp.Msg)
	return nil
}

func (r *statusRunner) renderStatusView(resp *resourcev1.GetResourceStatusResponse) {
	titleStyle := lipgloss.NewStyle().
		Foreground(ui.LocoCyan).
		Bold(true).
		MarginBottom(1)

	labelStyle := lipgloss.NewStyle().
		Foreground(ui.LocoDimGrey).
		Width(18)

	valueStyle := lipgloss.NewStyle().
		Foreground(ui.LocoWhite).
		Bold(true)

	blockStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.LocoOrange).
		Padding(1, 2).
		Margin(1, 2)

	var status, replicas string
	status = resp.CurrentDeployment.Status.String()
	replicas = fmt.Sprintf("%d", resp.CurrentDeployment.Replicas)

	url := "hostname management pending"

	content := fmt.Sprintf(
		"%s %s\n%s %s\n%s %s\n%s %s",
		labelStyle.Render("Service:"), valueStyle.Render(r.name),
		labelStyle.Render("Status:"), valueStyle.Render(status),
		labelStyle.Render("Replicas:"), valueStyle.Render(replicas),
		labelStyle.Render("URL:"), valueStyle.Render(url),
	)

	fmt.Fprintln(r.deps.Stdout, titleStyle.Render("Service Status"))
	fmt.Fprintln(r.deps.Stdout, blockStyle.Render(content))
}
