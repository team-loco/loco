package loco

import (
	"fmt"

	deploymentv1 "github.com/loco-team/loco/shared/proto/deployment/v1"
	resourcev1 "github.com/loco-team/loco/shared/proto/resource/v1"
	"github.com/loco-team/loco/shared/config"
)

// configToResourceSpec converts a LocoConfig to a proto ResourceSpec.
// This creates the global, immutable resource intent from the loco.toml configuration.
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

// configToResourceSpecV1 converts LocoConfig to ResourceSpec v0.1
func configToResourceSpecV1(cfg *config.LocoConfig) (*resourcev1.ResourceSpec, error) {
	// build routing config
	routing := &resourcev1.RoutingConfig{
		Port:        cfg.Routing.Port,
		PathPrefix:  cfg.Routing.PathPrefix,
		IdleTimeout: cfg.Routing.IdleTimeout,
	}

	// build observability config
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

	// build regions config map (RegionTarget)
	regions := make(map[string]*resourcev1.RegionTarget)
	firstRegion := true
	for regionName, resourceCfg := range cfg.RegionConfig {
		target := &resourcev1.RegionTarget{
			Enabled:     true, // all configured regions are enabled
			Primary:     firstRegion,
			Cpu:         resourceCfg.CPU,
			Memory:      resourceCfg.Memory,
			MinReplicas: resourceCfg.ReplicasMin,
			MaxReplicas: resourceCfg.ReplicasMax,
		}

		// initialize and set scalers if autoscaling is enabled
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

	// create ServiceSpec with the built configs
	serviceSpec := &resourcev1.ServiceSpec{
		Routing:       routing,
		Observability: observability,
		Regions:       regions,
	}

	spec := &resourcev1.ResourceSpec{
		Spec: &resourcev1.ResourceSpec_Service{
			Service: serviceSpec,
		},
	}

	return spec, nil
}
