package resource

import (
	"errors"
	"fmt"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/config"
)

var errNilConfig = errors.New("config cannot be nil")

var errSourceBuildsUnavailable = errors.New(
	"building from source is not available yet: pass --image with a public image reference",
)

func resolveImage(image string) (string, error) {
	if image == "" {
		return "", errSourceBuildsUnavailable
	}
	return image, nil
}

// configToResourceSpec converts a LocoConfig to a proto ResourceSpec.
func configToResourceSpec(cfg *config.LocoConfig, version string) (*resourcev1.ResourceSpec, error) {
	if cfg == nil {
		return nil, errNilConfig
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
	for regionName, resourceCfg := range cfg.RegionConfig {
		target := &resourcev1.RegionTarget{
			Enabled:     true,
			Primary:     regionName == cfg.Metadata.Region,
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
