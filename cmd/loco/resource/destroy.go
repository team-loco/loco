package resource

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
	resourcev1 "github.com/team-loco/loco/proto/loco/resource/v1"
	"github.com/team-loco/loco/proto/loco/resource/v1/resourcev1connect"
)

type destroyDeps struct {
	LoadSessionConfig func() (*session.SessionConfig, error)
	NewAPIClient      func(host, token string) *client.Client
	NewResourceClient func(host string) resourcev1connect.ResourceServiceClient
	Stdout            io.Writer
}

func buildDestroyCmd() *cobra.Command {
	deps := destroyDeps{
		LoadSessionConfig: session.Load,
		NewAPIClient:      client.NewClient,
		NewResourceClient: func(host string) resourcev1connect.ResourceServiceClient {
			return resourcev1connect.NewResourceServiceClient(httputil.NewHTTPClient(), host)
		},
		Stdout: os.Stdout,
	}
	return newDestroyCmd(deps)
}

func newDestroyCmd(deps destroyDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy <name>",
		Short: "Destroy a service",
		Long: `Destroy a service and all its resources.

Examples:
  loco service destroy myapp
  loco service destroy myapp --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]

			yes, err := cmd.Flags().GetBool("yes")
			if err != nil {
				return fmt.Errorf("error reading yes flag: %w", err)
			}

			// Confirmation prompt (before any API calls)
			if !yes {
				confirmed, confirmErr := ui.AskYesNo(fmt.Sprintf("Are you sure you want to destroy the service '%s'?", name))
				if confirmErr != nil {
					return confirmErr
				}
				if !confirmed {
					fmt.Fprintln(deps.Stdout, "Aborted.")
					return nil
				}
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
			slog.Debug("destroying service", "resource_id", resource.Id, "name", name)

			deleteReq := connect.NewRequest(&resourcev1.DeleteResourceRequest{
				ResourceId: resource.Id,
			})
			deleteReq.Header().Set("Authorization", authHeader)

			_, err = resourceClient.DeleteResource(ctx, deleteReq)
			if err != nil {
				slog.Error("failed to destroy service", "error", err)
				return fmt.Errorf("failed to destroy service '%s': %w", name, err)
			}

			successMsg := fmt.Sprintf("\n🎉 Service '%s' destroyed!", name)
			s := lipgloss.NewStyle().
				Bold(true).
				Foreground(ui.LocoLightGreen).
				Render(successMsg)

			fmt.Fprintln(deps.Stdout, s)

			return nil
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}
