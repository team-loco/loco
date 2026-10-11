package resource

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
	"google.golang.org/protobuf/encoding/protojson"
)

var statusJSON = protojson.MarshalOptions{Multiline: true, Indent: "  "}

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
  loco resource status myapp
  loco resource status myapp --output json`,
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

			resource := resourceResp.Msg.GetResource()
			slog.Debug("fetching service status", "resource_id", resource.GetId(), "name", name)

			// Get resource status
			statusReq := connect.NewRequest(&resourcev1.GetResourceStatusRequest{
				ResourceId: resource.GetId(),
			})
			statusReq.Header().Set("Authorization", authHeader)

			resp, err := resourceClient.GetResourceStatus(ctx, statusReq)
			if err != nil {
				slog.Error("failed to get service status", "error", err)
				return fmt.Errorf("failed to get service status: %w", err)
			}

			if output == outputJSON {
				out, err := statusJSON.Marshal(resp.Msg)
				if err != nil {
					return fmt.Errorf("failed to encode service status: %w", err)
				}
				_, err = fmt.Fprintln(deps.Stdout, string(out))
				return err
			}

			return renderStatusView(deps.Stdout, name, resp.Msg)
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().StringP("output", "o", "table", "Output format: table | json")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}

func renderStatusView(stdout io.Writer, name string, resp *resourcev1.GetResourceStatusResponse) error {
	titleStyle := lipgloss.NewStyle().
		Foreground(ui.Accent).
		Bold(true).
		MarginBottom(1)

	labelStyle := lipgloss.NewStyle().
		Foreground(ui.Fg3).
		Width(18)

	valueStyle := lipgloss.NewStyle().
		Foreground(ui.Fg).
		Bold(true)

	blockStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.Line2).
		Padding(1, 2).
		Margin(1, 2)

	var status, replicas string
	status = resp.GetCurrentDeployment().GetStatus().String()
	replicas = strconv.Itoa(int(resp.GetCurrentDeployment().GetReplicas()))

	resource := resp.GetResource()
	url := publicURL(resource)
	if url == "" {
		url = "none"
	}

	content := fmt.Sprintf(
		"%s %s\n%s %s\n%s %s\n%s %s",
		labelStyle.Render("Service:"), valueStyle.Render(name),
		labelStyle.Render("Status:"), valueStyle.Render(status),
		labelStyle.Render("Replicas:"), valueStyle.Render(replicas),
		labelStyle.Render("URL:"), valueStyle.Render(url),
	)

	title := titleStyle.Render("Service Status")
	if _, err := lipgloss.Fprintln(stdout, title); err != nil {
		return err
	}
	block := blockStyle.Render(content)
	_, err := lipgloss.Fprintln(stdout, block)
	return err
}

func publicURL(resource *resourcev1.Resource) string {
	domains := resource.GetDomains()
	for _, domain := range domains {
		if domain.GetIsPrimary() {
			return "https://" + domain.GetDomain()
		}
	}
	return ""
}
