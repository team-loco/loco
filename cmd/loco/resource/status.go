package resource

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/team-loco/loco/internal/rpc"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	notDeployedMessage    = "Not deployed to any environment."
	deploymentPhasePrefix = "DEPLOYMENT_PHASE_"
)

var (
	statusJSON   = protojson.MarshalOptions{Multiline: true, Indent: "  "}
	sectionStyle = lipgloss.NewStyle().MarginLeft(2)
	tableMargin  = lipgloss.NewStyle().Margin(0, 2, 1, 2)
)

type statusDeps struct {
	LoadSessionConfig func() (*session.SessionConfig, error)
	NewAPIClient      func(host, token string) *client.Client
	NewResourceClient func(host, token string) resourcev1connect.ResourceServiceClient
	Stdout            io.Writer
}

func buildStatusCmd() *cobra.Command {
	deps := statusDeps{
		LoadSessionConfig: session.Load,
		NewAPIClient:      client.NewClient,
		NewResourceClient: func(host, token string) resourcev1connect.ResourceServiceClient {
			resourceClients := rpc.ForHost(host, token)
			return resourceClients.Resource
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
			resourceClient := deps.NewResourceClient(host, locoToken.Token)

			// Get resource by name
			getReq := &resourcev1.GetResourceRequest{
				Key: &resourcev1.GetResourceRequest_NameKey{
					NameKey: &resourcev1.GetResourceNameKey{
						WorkspaceId: workspaceID,
						Name:        name,
					},
				},
			}

			resourceResp, err := resourceClient.GetResource(ctx, getReq)
			if err != nil {
				return fmt.Errorf("service '%s' not found: %w", name, err)
			}

			if output == outputJSON {
				out, err := statusJSON.Marshal(resourceResp)
				if err != nil {
					return fmt.Errorf("failed to encode service status: %w", err)
				}
				_, err = fmt.Fprintln(deps.Stdout, string(out))
				return err
			}

			resource := resourceResp.GetResource()
			return renderStatusView(deps.Stdout, name, resource)
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().StringP("output", "o", "table", "Output format: table | json")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}

func renderStatusView(stdout io.Writer, name string, resource *resourcev1.Resource) error {
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

	url := publicURL(resource)
	if url == "" {
		url = "none"
	}

	content := fmt.Sprintf(
		"%s %s\n%s %s",
		labelStyle.Render("Service:"), valueStyle.Render(name),
		labelStyle.Render("URL:"), valueStyle.Render(url),
	)

	title := titleStyle.Render("Service Status")
	if _, err := lipgloss.Fprintln(stdout, title); err != nil {
		return err
	}
	block := blockStyle.Render(content)
	if _, err := lipgloss.Fprintln(stdout, block); err != nil {
		return err
	}

	statuses := resource.GetEnvironmentStatuses()
	if len(statuses) == 0 {
		_, err := lipgloss.Fprintln(stdout, sectionStyle.Render(notDeployedMessage))
		return err
	}
	for _, environment := range statuses {
		if err := renderEnvironmentStatus(stdout, environment); err != nil {
			return err
		}
	}
	return nil
}

func renderEnvironmentStatus(stdout io.Writer, environment *resourcev1.EnvironmentStatus) error {
	nameStyle := lipgloss.NewStyle().Foreground(ui.Fg).Bold(true)
	status := environment.GetStatus()
	statusStyle := lipgloss.NewStyle().Foreground(statusColor(status))
	heading := nameStyle.Render(environment.GetEnvironmentName()) + "  " + statusStyle.Render(statusLabel(status))
	if _, err := lipgloss.Fprintln(stdout, sectionStyle.Render(heading)); err != nil {
		return err
	}

	rows := make([][]string, 0, len(environment.GetRegions()))
	for _, region := range environment.GetRegions() {
		ready := strconv.Itoa(int(region.GetReadyReplicas())) + "/" + strconv.Itoa(int(region.GetDesiredReplicas()))
		rows = append(rows, []string{
			region.GetRegion(),
			statusLabel(region.GetStatus()),
			ready,
			deploymentLabel(region.GetDeploymentStatus()),
			simplifyMessage(region.GetMessage()),
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
		Headers("REGION", "STATUS", "READY", "DEPLOYMENT", "MESSAGE").
		Rows(rows...).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerStyle
			}
			return cellStyle
		})
	_, err := lipgloss.Fprintln(stdout, tableMargin.Render(t.String()))
	return err
}

func statusLabel(status resourcev1.ResourceStatus) string {
	switch status {
	case resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY:
		return "Healthy"
	case resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING:
		return "Deploying"
	case resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED:
		return "Degraded"
	case resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE:
		return "Unavailable"
	case resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN:
		return "Unknown"
	case resourcev1.ResourceStatus_RESOURCE_STATUS_UNSPECIFIED:
		return "Not deployed"
	default:
		return status.String()
	}
}

func statusColor(status resourcev1.ResourceStatus) ui.Color {
	switch status {
	case resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY:
		return ui.Ok
	case resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING:
		return ui.Info
	case resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED:
		return ui.Warn
	case resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE:
		return ui.Bad
	case resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN:
		return ui.Fg3
	case resourcev1.ResourceStatus_RESOURCE_STATUS_UNSPECIFIED:
		return ui.Fg3
	default:
		return ui.Fg3
	}
}

func deploymentLabel(phase deploymentv1.DeploymentPhase) string {
	name := strings.TrimPrefix(phase.String(), deploymentPhasePrefix)
	return strings.ToLower(name)
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

func simplifyMessage(message string) string {
	if strings.Contains(message, "ImagePullBackOff") {
		return "Error: ImagePullBackOff"
	}
	if strings.Contains(message, "Failed to pull image") {
		return "Failed to pull image. Please check registry credentials and image path."
	}
	return message
}
