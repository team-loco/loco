package resource

import (
	"context"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	registryv1 "github.com/team-loco/loco/gen/go/loco/registry/v1"
	"github.com/team-loco/loco/gen/go/loco/registry/v1/registryv1connect"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/ui"
)

// buildAndPushImage builds (or validates a pre-built) Docker image and pushes it to the registry.
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
