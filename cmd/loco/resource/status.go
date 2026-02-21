package resource

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"

	"connectrpc.com/connect"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
	"github.com/team-loco/loco/internal/httputil"
	resourcev1 "github.com/team-loco/loco/proto/loco/resource/v1"
	"github.com/team-loco/loco/proto/loco/resource/v1/resourcev1connect"
)

type statusDeps struct {
	LoadSessionConfig func() (*session.SessionConfig, error)
	NewAPIClient      func(host, token string) *client.Client
	NewResourceClient func(host string) resourcev1connect.ResourceServiceClient
	Stdout            io.Writer
}

func buildStatusCmd() *cobra.Command {
	deps := statusDeps{
		LoadSessionConfig: session.Load,
		NewAPIClient:      client.NewClient,
		NewResourceClient: func(host string) resourcev1connect.ResourceServiceClient {
			return resourcev1connect.NewResourceServiceClient(httputil.NewHTTPClient(), host)
		},
		Stdout: os.Stdout,
	}
	return newStatusCmd(deps)
}

func newStatusCmd(deps statusDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show service status",
		Long: `Display the current status of a service.

Examples:
  loco service status myapp
  loco service status myapp --output json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]

			output, err := cmd.Flags().GetString("output")
			if err != nil {
				return fmt.Errorf("error reading output flag: %w", err)
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
			slog.Debug("fetching service status", "resource_id", resource.Id, "name", name)

			// Get resource status
			statusReq := connect.NewRequest(&resourcev1.GetResourceStatusRequest{
				ResourceId: resource.Id,
			})
			statusReq.Header().Set("Authorization", authHeader)

			resp, err := resourceClient.GetResourceStatus(ctx, statusReq)
			if err != nil {
				slog.Error("failed to get service status", "error", err)
				return fmt.Errorf("failed to get service status: %w", err)
			}

			if output == "json" {
				encoder := json.NewEncoder(deps.Stdout)
				encoder.SetIndent("", "  ")
				return encoder.Encode(resp.Msg)
			}

			// Render table view
			renderStatusView(deps.Stdout, name, resp.Msg)
			return nil
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().StringP("output", "o", "table", "Output format: table | json")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}

func renderStatusView(stdout io.Writer, name string, resp *resourcev1.GetResourceStatusResponse) {
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
		labelStyle.Render("Service:"), valueStyle.Render(name),
		labelStyle.Render("Status:"), valueStyle.Render(status),
		labelStyle.Render("Replicas:"), valueStyle.Render(replicas),
		labelStyle.Render("URL:"), valueStyle.Render(url),
	)

	fmt.Fprintln(stdout, titleStyle.Render("Service Status"))
	fmt.Fprintln(stdout, blockStyle.Render(content))
}
