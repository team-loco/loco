package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"

	"connectrpc.com/connect"
	"github.com/charmbracelet/lipgloss"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/service/internal"
	"github.com/team-loco/loco/internal/ui"
	"github.com/team-loco/loco/shared/config"
	deploymentv1 "github.com/team-loco/loco/shared/proto/deployment/v1"
	registryv1 "github.com/team-loco/loco/shared/proto/registry/v1"
	resourcev1 "github.com/team-loco/loco/shared/proto/resource/v1"
)

func buildDeployCmd(deps *internal.ServiceDeps) *cobra.Command {
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
			name := args[0]
			runner := &deployRunner{
				deps: deps,
				name: name,
			}
			return runner.Run(cmd)
		},
	}

	cmd.Flags().StringP("config", "c", "", "Path to loco.toml config file (optional)")
	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().StringP("image", "i", "", "Use existing image instead of building")
	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().Bool("wait", false, "Wait for deployment to complete")

	return cmd
}

type deployRunner struct {
	deps *internal.ServiceDeps
	name string
}

func (r *deployRunner) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	resolver := internal.NewContextResolver(r.deps)
	gatherer := internal.NewConfigGatherer(r.deps)

	// Resolve org and workspace
	orgID, err := resolver.ResolveOrgID(ctx, cmd)
	if err != nil {
		return err
	}

	workspaceID, err := resolver.ResolveWorkspaceID(ctx, cmd, orgID)
	if err != nil {
		return err
	}

	// Load or gather config
	loadedCfg, err := r.loadOrGatherConfig(ctx, cmd, gatherer)
	if err != nil {
		return err
	}

	// Override name from positional arg
	loadedCfg.Config.Metadata.Name = r.name

	// Validate and fill defaults
	if validateErr := config.Validate(loadedCfg.Config); validateErr != nil {
		return fmt.Errorf("config validation failed: %w", validateErr)
	}
	config.FillSensibleDefaults(loadedCfg.Config)

	cfgValid := lipgloss.NewStyle().Render("Config validated. Beginning deployment!")
	fmt.Fprintln(r.deps.Stdout, cfgValid)

	// Get or create resource
	resourceID, err := r.getOrCreateResource(ctx, workspaceID, loadedCfg.Config, gatherer)
	if err != nil {
		return err
	}

	// Build and push image
	imageID, _ := cmd.Flags().GetString("image")
	imageName, err := r.buildAndPushImage(ctx, cmd, orgID, workspaceID, resourceID, loadedCfg, imageID)
	if err != nil {
		return err
	}

	// Create deployment
	wait, _ := cmd.Flags().GetBool("wait")
	if err := r.createDeployment(ctx, resourceID, imageName, loadedCfg.Config, wait); err != nil {
		return err
	}

	// Success message
	successMsg := "\n🎉 Deployment scheduled!"
	if wait {
		successMsg = "\n🎉 Service deployed!"
	}
	s := lipgloss.NewStyle().Bold(true).Foreground(ui.LocoLightGreen).Render(successMsg)
	fmt.Fprintln(r.deps.Stdout, s)

	tip := lipgloss.NewStyle().Foreground(ui.LocoOrange).Render("\nTip: Keep tabs on your service using `loco service status " + r.name + "`")
	fmt.Fprintln(r.deps.Stdout, tip)

	return nil
}

func (r *deployRunner) loadOrGatherConfig(ctx context.Context, cmd *cobra.Command, gatherer *internal.ConfigGatherer) (*config.LoadedConfig, error) {
	configPath, _ := cmd.Flags().GetString("config")
	if configPath == "" {
		configPath = "loco.toml"
	}

	// Try to load config file
	loadedCfg, err := r.deps.LoadLocoConfig(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No config file - gather interactively
			slog.Info("no loco.toml found, gathering config interactively")
			return gatherer.GatherDeployConfig(ctx, r.name)
		}
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return loadedCfg, nil
}

func (r *deployRunner) getOrCreateResource(ctx context.Context, workspaceID int64, cfg *config.LocoConfig, gatherer *internal.ConfigGatherer) (int64, error) {
	// Check if resource already exists
	getReq := connect.NewRequest(&resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_NameKey{
			NameKey: &resourcev1.GetResourceNameKey{
				WorkspaceId: workspaceID,
				Name:        cfg.Metadata.Name,
			},
		},
	})
	getReq.Header().Set("Authorization", r.deps.AuthHeader())

	resp, err := r.deps.GetResource(ctx, getReq)
	if err == nil {
		slog.Debug("found existing resource", "resource_id", resp.Msg.Resource.Id, "name", resp.Msg.Resource.Name)
		return resp.Msg.Resource.Id, nil
	}

	if connect.CodeOf(err) != connect.CodeNotFound {
		return 0, fmt.Errorf("failed to get resource '%s': %w", cfg.Metadata.Name, err)
	}

	// Resource doesn't exist - create it
	slog.Info("no existing resource found, creating new one")

	// Resolve domain input
	domainInput, err := gatherer.ResolveDomainInput(ctx, cfg)
	if err != nil {
		return 0, err
	}

	// Convert config to resource spec
	resourceSpec, err := configToResourceSpec(cfg, "v1")
	if err != nil {
		return 0, fmt.Errorf("failed to convert config to resource spec: %w", err)
	}

	createReq := connect.NewRequest(&resourcev1.CreateResourceRequest{
		WorkspaceId: workspaceID,
		Name:        cfg.Metadata.Name,
		Type:        resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Domain:      domainInput,
		Spec:        resourceSpec,
	})
	createReq.Header().Set("Authorization", r.deps.AuthHeader())

	createResp, err := r.deps.CreateResource(ctx, createReq)
	if err != nil {
		return 0, fmt.Errorf("failed to create resource: %w", err)
	}

	slog.Debug("created resource", "resourceId", createResp.Msg.ResourceId)
	return createResp.Msg.ResourceId, nil
}

func (r *deployRunner) buildAndPushImage(ctx context.Context, cmd *cobra.Command, orgID, workspaceID, resourceID int64, loadedCfg *config.LoadedConfig, imageID string) (string, error) {
	dockerClient, err := r.deps.NewDockerClient(loadedCfg)
	if err != nil {
		return "", fmt.Errorf("failed to create docker client: %w", err)
	}
	defer dockerClient.Close()

	imageBase := "registry.gitlab.com/locomotive-group/loco-ecr"
	imageName := dockerClient.GenerateImageTag(imageBase, orgID, workspaceID, resourceID)
	dockerClient.ImageName = imageName
	slog.Debug("generated image name", "imageBase", imageBase, "imageName", imageName)

	var steps []ui.Step

	if imageID != "" {
		// Validate and tag existing image
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
		// Build image
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

	// Push image
	steps = append(steps, ui.Step{
		Title: "Push image to registry",
		Run: func(logf func(string)) error {
			// Get registry credentials
			tokenReq := connect.NewRequest(&registryv1.GetGitlabTokenRequest{})
			tokenReq.Header().Set("Authorization", r.deps.AuthHeader())

			tokenResp, tokenErr := r.deps.GetGitlabToken(ctx, tokenReq)
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

func (r *deployRunner) createDeployment(ctx context.Context, resourceID int64, imageName string, cfg *config.LocoConfig, wait bool) error {
	steps := []ui.Step{
		{
			Title: "Create deployment",
			Run: func(logf func(string)) error {
				return r.doCreateDeployment(ctx, resourceID, imageName, cfg, logf, wait)
			},
		},
	}

	return ui.RunSteps(steps)
}

func (r *deployRunner) doCreateDeployment(ctx context.Context, resourceID int64, imageName string, cfg *config.LocoConfig, logf func(string), wait bool) error {
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

	// Get primary region config
	primaryRegion := cfg.RegionConfig[cfg.Metadata.Region]

	var scalers *deploymentv1.Scalers
	if primaryRegion.EnableAutoScaling {
		scalers = &deploymentv1.Scalers{
			Enabled:      true,
			CpuTarget:    &primaryRegion.CPUTarget,
			MemoryTarget: &primaryRegion.ScalersMemTarget,
		}
	}

	// Build env from file and variables
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
	createReq.Header().Set("Authorization", r.deps.AuthHeader())

	resp, err := r.deps.CreateDeployment(ctx, createReq)
	if err != nil {
		logf(fmt.Sprintf("Failed to create deployment: %v", err))
		return err
	}

	deploymentID := resp.Msg.DeploymentId
	logf(fmt.Sprintf("Created deployment with version: %d", deploymentID))

	if wait {
		logf("Waiting for deployment to complete...")
		watchReq := connect.NewRequest(&deploymentv1.WatchDeploymentRequest{
			DeploymentId: deploymentID,
		})
		watchReq.Header().Set("Authorization", r.deps.AuthHeader())

		stream, err := r.deps.WatchDeployment(ctx, watchReq)
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
