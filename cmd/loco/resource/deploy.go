package resource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/docker"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
	deploymentv1 "github.com/team-loco/loco/proto/loco/deployment/v1"
	"github.com/team-loco/loco/proto/loco/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/proto/loco/domain/v1/domainv1connect"
	registryv1 "github.com/team-loco/loco/proto/loco/registry/v1"
	"github.com/team-loco/loco/proto/loco/registry/v1/registryv1connect"
	resourcev1 "github.com/team-loco/loco/proto/loco/resource/v1"
	"github.com/team-loco/loco/proto/loco/resource/v1/resourcev1connect"
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
			imageID, err := cmd.Flags().GetString("image")
			if err != nil {
				return fmt.Errorf("failed to get image flag: %w", err)
			}
			imageName, err := buildAndPushImage(ctx, deps, registryClient, authHeader, orgID, workspaceID, resourceID, loadedCfg, imageID)
			if err != nil {
				return err
			}

			// Create deployment
			wait, err := cmd.Flags().GetBool("wait")
			if err != nil {
				return fmt.Errorf("failed to get wait flag: %w", err)
			}
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

func buildAndPushImage(
	ctx context.Context,
	deps deployDeps,
	registryClient registryv1connect.RegistryServiceClient,
	authHeader string,
	orgID, workspaceID, resourceID string,
	loadedCfg *config.LoadedConfig,
	imageID string,
) (string, error) {
	dockerClient, err := deps.NewDockerClient(loadedCfg)
	if err != nil {
		return "", err
	}
	defer dockerClient.Close()

	imageBase := "registry.gitlab.com/locomotive-group/loco-ecr"
	imageName := dockerClient.GenerateImageTag(imageBase, orgID, workspaceID, resourceID)
	slog.Debug("generated image name", "imageBase", imageBase, "imageName", imageName)

	var steps []ui.Step

	if imageID != "" {
		steps = append(steps, ui.Step{
			Title: "Validate and tag Docker image",
			Run: func(logf func(string)) error {
				if validateErr := dockerClient.ValidateImage(ctx, imageID, logf); validateErr != nil {
					return fmt.Errorf("image validation failed: %w", validateErr)
				}
				if tagErr := dockerClient.ImageTag(ctx, imageID); tagErr != nil {
					return fmt.Errorf("failed to tag image: %w", tagErr)
				}
				return nil
			},
		})
	} else {
		steps = append(steps, ui.Step{
			Title: "Build Docker image",
			Run: func(logf func(string)) error {
				if buildErr := dockerClient.BuildImage(ctx, logf); buildErr != nil {
					return fmt.Errorf("docker build failed: %w", buildErr)
				}
				return nil
			},
		})
	}

	steps = append(steps, ui.Step{
		Title: "Validate image",
		Run: func(logf func(string)) error {
			if validateErr := dockerClient.ValidateImage(ctx, imageName, logf); validateErr != nil {
				return fmt.Errorf("image validation failed: %w", validateErr)
			}
			return nil
		},
	})

	steps = append(steps, ui.Step{
		Title: "Push image to registry",
		Run: func(logf func(string)) error {
			tokenReq := connect.NewRequest(&registryv1.GetGitlabTokenRequest{})
			tokenReq.Header().Set("Authorization", authHeader)

			tokenResp, tokenErr := registryClient.GetGitlabToken(ctx, tokenReq)
			if tokenErr != nil {
				return fmt.Errorf("failed to fetch registry credentials: %w", tokenErr)
			}

			if imageID != "" {
				if tagErr := dockerClient.ImageTag(ctx, imageID); tagErr != nil {
					return fmt.Errorf("failed to tag image: %w", tagErr)
				}
			}

			if pushErr := dockerClient.PushImage(ctx, logf, tokenResp.Msg.GetUsername(), tokenResp.Msg.GetToken()); pushErr != nil {
				return fmt.Errorf("docker push failed: %w", pushErr)
			}
			return nil
		},
	})

	if err := ui.RunSteps(steps); err != nil {
		return "", err
	}

	return imageName, nil
}

func createDeployment(
	ctx context.Context,
	deploymentClient deploymentv1connect.DeploymentServiceClient,
	authHeader string,
	resourceID string,
	imageName string,
	cfg *config.LocoConfig,
	wait bool,
) error {
	steps := []ui.Step{
		{
			Title: "Create deployment",
			Run: func(logf func(string)) error {
				return doCreateDeployment(ctx, deploymentClient, authHeader, resourceID, imageName, cfg, logf, wait)
			},
		},
	}

	return ui.RunSteps(steps)
}

func doCreateDeployment(
	ctx context.Context,
	deploymentClient deploymentv1connect.DeploymentServiceClient,
	authHeader string,
	resourceID string,
	imageName string,
	cfg *config.LocoConfig,
	logf func(string),
	wait bool,
) error {
	buildSource := &deploymentv1.BuildSource{
		Type:           cfg.Build.Type,
		Image:          imageName,
		DockerfilePath: &cfg.Build.DockerfilePath,
	}

	healthCheck := &deploymentv1.HealthCheckConfig{
		Path:                cfg.Health.Path,
		InitialDelaySeconds: cfg.Health.StartupGracePeriod,
		IntervalSeconds:     cfg.Health.Interval,
		TimeoutSeconds:      cfg.Health.Timeout,
		FailureThreshold:    cfg.Health.FailThreshold,
	}

	primaryRegion := cfg.RegionConfig[cfg.Metadata.Region]

	var scalers *deploymentv1.Scalers
	if primaryRegion.EnableAutoScaling {
		scalers = &deploymentv1.Scalers{
			Enabled:      true,
			CpuTarget:    &primaryRegion.CPUTarget,
			MemoryTarget: &primaryRegion.ScalersMemTarget,
		}
	}

	env := make(map[string]string)
	if cfg.Env.File != "" {
		f, openErr := os.Open(cfg.Env.File)
		if openErr != nil {
			return fmt.Errorf("failed to open env file %s: %w", cfg.Env.File, openErr)
		}
		defer f.Close()
		parsed, parseErr := godotenv.Parse(f)
		if parseErr != nil {
			return fmt.Errorf("failed to parse env file %s: %w", cfg.Env.File, parseErr)
		}
		maps.Copy(env, parsed)
	}
	if cfg.Env.Variables != nil {
		maps.Copy(env, cfg.Env.Variables)
	}

	serviceDeploymentSpec := &deploymentv1.ServiceDeploymentSpec{
		Build:       buildSource,
		HealthCheck: healthCheck,
		Port:        cfg.Routing.Port,
		Cpu:         &primaryRegion.CPU,
		Memory:      &primaryRegion.Memory,
		MinReplicas: &primaryRegion.ReplicasMin,
		MaxReplicas: &primaryRegion.ReplicasMax,
		Scalers:     scalers,
		Env:         env,
	}

	deploymentSpec := &deploymentv1.DeploymentSpec{
		Spec: &deploymentv1.DeploymentSpec_Service{
			Service: serviceDeploymentSpec,
		},
	}

	createReq := connect.NewRequest(&deploymentv1.CreateDeploymentRequest{
		ResourceId: resourceID,
		Spec:       deploymentSpec,
	})
	createReq.Header().Set("Authorization", authHeader)

	resp, err := deploymentClient.CreateDeployment(ctx, createReq)
	if err != nil {
		logf(fmt.Sprintf("Failed to create deployment: %v", err))
		return err
	}

	deploymentID := resp.Msg.DeploymentId
	logf(fmt.Sprintf("Created deployment with version: %s", deploymentID))

	if wait {
		logf("Waiting for deployment to complete...")
		watchReq := connect.NewRequest(&deploymentv1.WatchDeploymentRequest{
			DeploymentId: deploymentID,
		})
		watchReq.Header().Set("Authorization", authHeader)

		stream, err := deploymentClient.WatchDeployment(ctx, watchReq)
		if err != nil {
			return fmt.Errorf("failed to watch deployment: %w", err)
		}

		for stream.Receive() {
			event := stream.Msg()
			logf(fmt.Sprintf("[%s] %s", event.Status, event.Message))
			if event.Status == deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED && event.Message != "" {
				return errors.New(event.Message)
			}
		}

		if err := stream.Err(); err != nil {
			return fmt.Errorf("deployment stream error: %w", err)
		}
	}

	return nil
}

// configToResourceSpec converts a LocoConfig to a proto ResourceSpec.
func configToResourceSpec(cfg *config.LocoConfig, version string) (*resourcev1.ResourceSpec, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	switch version {
	case "v1":
		return configToResourceSpecV1(cfg)
	default:
		return nil, fmt.Errorf("unsupported spec version: %s", version)
	}
}

func configToResourceSpecV1(cfg *config.LocoConfig) (*resourcev1.ResourceSpec, error) {
	routing := &resourcev1.RoutingConfig{
		Port:        cfg.Routing.Port,
		PathPrefix:  cfg.Routing.PathPrefix,
		IdleTimeout: cfg.Routing.IdleTimeout,
	}

	observability := &resourcev1.ObservabilityConfig{
		Logging: &resourcev1.LoggingConfig{
			Enabled:         cfg.Obs.Logging.Enabled,
			RetentionPeriod: cfg.Obs.Logging.RetentionPeriod,
			Structured:      cfg.Obs.Logging.Structured,
		},
		Metrics: &resourcev1.MetricsConfig{
			Enabled: cfg.Obs.Metrics.Enabled,
			Path:    cfg.Obs.Metrics.Path,
			Port:    cfg.Obs.Metrics.Port,
		},
		Tracing: &resourcev1.TracingConfig{
			Enabled:    cfg.Obs.Tracing.Enabled,
			SampleRate: cfg.Obs.Tracing.SampleRate,
			Tags:       cfg.Obs.Tracing.Tags,
		},
	}

	regions := make(map[string]*resourcev1.RegionTarget)
	firstRegion := true
	for regionName, resourceCfg := range cfg.RegionConfig {
		target := &resourcev1.RegionTarget{
			Enabled:     true,
			Primary:     firstRegion,
			Cpu:         resourceCfg.CPU,
			Memory:      resourceCfg.Memory,
			MinReplicas: resourceCfg.ReplicasMin,
			MaxReplicas: resourceCfg.ReplicasMax,
		}

		if resourceCfg.EnableAutoScaling {
			scalers := &deploymentv1.Scalers{
				Enabled:      true,
				CpuTarget:    &resourceCfg.CPUTarget,
				MemoryTarget: &resourceCfg.ScalersMemTarget,
			}
			target.Scalers = scalers
		}

		regions[regionName] = target
		firstRegion = false
	}

	serviceSpec := &resourcev1.ServiceSpec{
		Routing:       routing,
		Observability: observability,
		Regions:       regions,
	}

	return &resourcev1.ResourceSpec{
		Spec: &resourcev1.ResourceSpec_Service{
			Service: serviceSpec,
		},
	}, nil
}
