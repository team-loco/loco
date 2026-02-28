package resource

import (
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
	resourcev1 "github.com/team-loco/loco/proto/loco/resource/v1"
	"github.com/team-loco/loco/proto/loco/resource/v1/resourcev1connect"
)

type envDeps struct {
	LoadSessionConfig func() (*session.SessionConfig, error)
	NewAPIClient      func(host, token string) *client.Client
	NewResourceClient func(host string) resourcev1connect.ResourceServiceClient
	Stdout            io.Writer
}

func buildEnvCmd() *cobra.Command {
	deps := envDeps{
		LoadSessionConfig: session.Load,
		NewAPIClient:      client.NewClient,
		NewResourceClient: func(host string) resourcev1connect.ResourceServiceClient {
			return resourcev1connect.NewResourceServiceClient(httputil.NewHTTPClient(), host)
		},
		Stdout: os.Stdout,
	}
	return newEnvCmd(deps)
}

func newEnvCmd(deps envDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env <name>",
		Short: "Manage service environment variables",
		Long: `Manage environment variables for a service.

Examples:
  loco service env myapp set KEY=VALUE
  loco service env myapp set KEY1=VALUE1 KEY2=VALUE2
  loco service env myapp --env-file .env
  loco service env myapp --set KEY=VALUE --env-file .env`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]
			setArgs := args[1:]

			envFile, err := cmd.Flags().GetString("env-file")
			if err != nil {
				return fmt.Errorf("error reading env-file flag: %w", err)
			}

			setVars, err := cmd.Flags().GetStringSlice("set")
			if err != nil {
				return fmt.Errorf("error reading set flag: %w", err)
			}

			// Build env vars map
			envVars := make(map[string]string)

			// Load from file first
			if envFile != "" {
				f, openErr := os.Open(envFile)
				if openErr != nil {
					return fmt.Errorf("failed to open env file %s: %w", envFile, openErr)
				}
				defer f.Close()
				parsed, parseErr := godotenv.Parse(f)
				if parseErr != nil {
					return fmt.Errorf("failed to parse env file %s: %w", envFile, parseErr)
				}
				maps.Copy(envVars, parsed)
			}

			// Parse --set flags
			for _, setVar := range setVars {
				parts := strings.SplitN(setVar, "=", 2)
				if len(parts) != 2 {
					return fmt.Errorf("invalid --set format: %s, expected KEY=VALUE", setVar)
				}
				envVars[parts[0]] = parts[1]
			}

			// Parse positional KEY=VALUE args
			for _, arg := range setArgs {
				parts := strings.SplitN(arg, "=", 2)
				if len(parts) != 2 {
					return fmt.Errorf("invalid format: %s, expected KEY=VALUE", arg)
				}
				envVars[parts[0]] = parts[1]
			}

			if len(envVars) == 0 {
				return fmt.Errorf("no environment variables to sync. Use positional args (KEY=VALUE), --set, or --env-file")
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
			slog.Debug("updating environment variables", "resource_id", resource.Id, "name", name)

			envReq := connect.NewRequest(&resourcev1.UpdateResourceEnvRequest{
				ResourceId: resource.Id,
				Env:        envVars,
			})
			envReq.Header().Set("Authorization", authHeader)

			_, err = resourceClient.UpdateResourceEnv(ctx, envReq)
			if err != nil {
				slog.Error("failed to update environment variables", "error", err)
				return fmt.Errorf("failed to update environment variables for service '%s': %w", name, err)
			}

			s := lipgloss.NewStyle().
				Bold(true).
				Foreground(ui.LocoLightGreen).
				Render(fmt.Sprintf("\n🎉 Environment variables synced for service %s", name))
			fmt.Fprintln(deps.Stdout, s)

			return nil
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().String("env-file", "", "Path to .env file")
	cmd.Flags().StringSlice("set", []string{}, "Set environment variables (e.g. --set KEY1=VALUE1 --set KEY2=VALUE2)")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}
