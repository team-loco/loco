package resource

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/gen/go/loco/domain/v1/domainv1connect"
	"github.com/team-loco/loco/gen/go/loco/registry/v1/registryv1connect"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/docker"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
)

type deployDeps struct {
	LoadSessionConfig   func() (*session.SessionConfig, error)
	LoadLocoConfig      func(path string) (*config.LoadedConfig, error)
	NewAPIClient        func(host, token string) *client.Client
	NewResourceClient   func(host string) resourcev1connect.ResourceServiceClient
	NewDeploymentClient func(host string) deploymentv1connect.DeploymentServiceClient
	NewDomainClient     func(host string) domainv1connect.DomainServiceClient
	NewRegistryClient   func(host string) registryv1connect.RegistryServiceClient
	NewDockerClient     func(cfg *config.LoadedConfig) (*docker.DockerClient, error)
	SelectFromList      func(title string, options []ui.SelectOption) (any, error)
	Stdout              io.Writer
}

func buildDeployCmd() *cobra.Command {
	deps := deployDeps{
		LoadSessionConfig: session.Load,
		LoadLocoConfig:    config.Load,
		NewAPIClient:      client.NewClient,
		NewResourceClient: func(host string) resourcev1connect.ResourceServiceClient {
			return resourcev1connect.NewResourceServiceClient(httputil.NewHTTPClient(), host)
		},
		NewDeploymentClient: func(host string) deploymentv1connect.DeploymentServiceClient {
			return deploymentv1connect.NewDeploymentServiceClient(httputil.NewHTTPClient(), host)
		},
		NewDomainClient: func(host string) domainv1connect.DomainServiceClient {
			return domainv1connect.NewDomainServiceClient(httputil.NewHTTPClient(), host)
		},
		NewRegistryClient: func(host string) registryv1connect.RegistryServiceClient {
			return registryv1connect.NewRegistryServiceClient(httputil.NewHTTPClient(), host)
		},
		NewDockerClient: docker.NewClient,
		SelectFromList:  ui.SelectFromList,
		Stdout:          os.Stdout,
	}
	return newDeployCmd(deps)
}

func newDeployCmd(deps deployDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy <name>",
		Short: "Deploy a service to Loco",
		Long: `Deploy a service to Loco.

If a loco.toml config file exists in the current directory (or specified via --config),
it will be used for configuration. Otherwise, you will be prompted interactively for
required values like region and domain.

Examples:
  loco service deploy myapp
  loco service deploy myapp --config ./loco.toml
  loco service deploy myapp --wait
  loco service deploy myapp --image myregistry/myimage:tag`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]

			imageID, err := cmd.Flags().GetString("image")
			if err != nil {
				return fmt.Errorf("failed to get image flag: %w", err)
			}
			wait, err := cmd.Flags().GetBool("wait")
			if err != nil {
				return fmt.Errorf("failed to get wait flag: %w", err)
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
			authHeader := fmt.Sprintf("Bearer %s", locoToken.Token)

			// Resolve org and workspace IDs
			apiClient := deps.NewAPIClient(host, locoToken.Token)
			orgID, err := resolveOrgID(ctx, cmd, deps.LoadSessionConfig, apiClient)
			if err != nil {
				return err
			}

			workspaceID, err := resolveWorkspaceID(ctx, cmd, deps.LoadSessionConfig, apiClient)
			if err != nil {
				return err
			}

			// Load config file
			loadedCfg, err := loadDeployConfig(cmd, deps)
			if err != nil {
				return err
			}

			// Override name from positional arg
			loadedCfg.Config.Metadata.Name = name

			// Validate and fill defaults
			if validateErr := config.Validate(loadedCfg.Config); validateErr != nil {
				return fmt.Errorf("config validation failed: %w", validateErr)
			}
			config.FillSensibleDefaults(loadedCfg.Config)

			cfgValid := lipgloss.NewStyle().Render("Config validated. Beginning deployment!")
			fmt.Fprintln(deps.Stdout, cfgValid)

			// Create clients
			resourceClient := deps.NewResourceClient(host)
			deploymentClient := deps.NewDeploymentClient(host)
			domainClient := deps.NewDomainClient(host)
			registryClient := deps.NewRegistryClient(host)

			// Get or create resource
			resourceID, err := getOrCreateResource(ctx, resourceClient, domainClient, deps.SelectFromList, authHeader, workspaceID, loadedCfg.Config)
			if err != nil {
				return err
			}

			// Build and push image
			imageName, err := buildAndPushImage(ctx, deps, registryClient, authHeader, orgID, workspaceID, resourceID, loadedCfg, imageID)
			if err != nil {
				return err
			}

			// Create deployment
			if err := createDeployment(ctx, deploymentClient, authHeader, resourceID, imageName, loadedCfg.Config, wait); err != nil {
				return err
			}

			// Success message
			successMsg := "\n🎉 Deployment scheduled!"
			if wait {
				successMsg = "\n🎉 Service deployed!"
			}
			s := lipgloss.NewStyle().Bold(true).Foreground(ui.LocoLightGreen).Render(successMsg)
			fmt.Fprintln(deps.Stdout, s)

			tip := lipgloss.NewStyle().Foreground(ui.LocoOrange).Render("\nTip: Keep tabs on your service using `loco service status " + name + "`")
			fmt.Fprintln(deps.Stdout, tip)

			return nil
		},
	}

	cmd.Flags().StringP("config", "c", "", "Path to loco.toml config file (optional)")
	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().StringP("image", "i", "", "Use existing image instead of building")
	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().Bool("wait", false, "Wait for any replicas to fully scale out.")

	return cmd
}

func loadDeployConfig(cmd *cobra.Command, deps deployDeps) (*config.LoadedConfig, error) {
	configPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return nil, fmt.Errorf("failed to get config flag: %w", err)
	}
	if configPath == "" {
		configPath = "loco.toml"
	}

	loadedCfg, err := deps.LoadLocoConfig(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no loco.toml found at %s", configPath)
		}
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return loadedCfg, nil
}

func getOrCreateResource(
	ctx context.Context,
	resourceClient resourcev1connect.ResourceServiceClient,
	domainClient domainv1connect.DomainServiceClient,
	selectFromList func(title string, options []ui.SelectOption) (any, error),
	authHeader string,
	workspaceID string,
	cfg *config.LocoConfig,
) (string, error) {
	// Check if resource already exists
	getReq := connect.NewRequest(&resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_NameKey{
			NameKey: &resourcev1.GetResourceNameKey{
				WorkspaceId: workspaceID,
				Name:        cfg.Metadata.Name,
			},
		},
	})
	getReq.Header().Set("Authorization", authHeader)

	resp, err := resourceClient.GetResource(ctx, getReq)
	if err == nil {
		slog.Debug("found existing resource", "resource_id", resp.Msg.Resource.Id, "name", resp.Msg.Resource.Name)
		return resp.Msg.Resource.Id, nil
	}

	if connect.CodeOf(err) != connect.CodeNotFound {
		return "", fmt.Errorf("failed to get resource '%s': %w", cfg.Metadata.Name, err)
	}

	// Resource doesn't exist - create it
	slog.Info("no existing resource found, creating new one")

	// Resolve domain input
	domainInput, err := resolveDomainInput(ctx, domainClient, selectFromList, authHeader, cfg)
	if err != nil {
		return "", err
	}

	// Convert config to resource spec
	resourceSpec, err := configToResourceSpec(cfg, "v1")
	if err != nil {
		return "", fmt.Errorf("failed to convert config to resource spec: %w", err)
	}

	createReq := connect.NewRequest(&resourcev1.CreateResourceRequest{
		WorkspaceId: workspaceID,
		Name:        cfg.Metadata.Name,
		Type:        resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Domain:      domainInput,
		Spec:        resourceSpec,
	})
	createReq.Header().Set("Authorization", authHeader)

	createResp, err := resourceClient.CreateResource(ctx, createReq)
	if err != nil {
		return "", fmt.Errorf("failed to create resource: %w", err)
	}

	slog.Debug("created resource", "resourceId", createResp.Msg.ResourceId)
	return createResp.Msg.ResourceId, nil
}
