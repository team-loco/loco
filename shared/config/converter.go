package config

import (
	"fmt"

	deploymentv1 "github.com/loco-team/loco/shared/proto/deployment/v1"
	resourcev1 "github.com/loco-team/loco/shared/proto/resource/v1"
)

// ConfigToResourceSpec converts a LocoConfig to a proto ResourceSpec.
// This creates the global, immutable resource intent from the loco.toml configuration.
func ConfigToResourceSpec(cfg *LocoConfig, version string) (*resourcev1.ResourceSpec, error) {
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

// ConfigToDeploymentSpec is a convenience function for CLI that converts LocoConfig directly to DeploymentSpec.
// It uses the first region as the deployment target.
// In the API, use CreateDeploymentSpec(resourceSpec, regionName, image, path) instead.
func ConfigToDeploymentSpec(cfg *LocoConfig, version string, imageName string) (*deploymentv1.DeploymentSpec, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Convert config to resource spec first
	resourceSpec, err := ConfigToResourceSpec(cfg, version)
	if err != nil {
		return nil, err
	}

	// Find first region (primary)
	var regionName string
	for name, target := range resourceSpec.Regions {
		if target.Primary {
			regionName = name
			break
		}
	}

	if regionName == "" {
		// fallback to first region
		for name := range resourceSpec.Regions {
			regionName = name
			break
		}
	}

	if regionName == "" {
		return nil, fmt.Errorf("no regions configured")
	}

	// Create deployment spec for that region
	return CreateDeploymentSpec(resourceSpec, regionName, imageName, cfg.Health.Path)
}

// CreateDeploymentSpec creates an immutable deployment snapshot from a ResourceSpec.
// It merges global config with region-specific overrides and captures CPU/Memory/Replicas
// as they are at deployment time.
func CreateDeploymentSpec(
	resourceSpec *resourcev1.ResourceSpec,
	regionName string,
	imageName string,
	healthCheckPath string,
) (*deploymentv1.DeploymentSpec, error) {
	if resourceSpec == nil {
		return nil, fmt.Errorf("resourceSpec cannot be nil")
	}

	// fetch region target
	regionTarget, ok := resourceSpec.Regions[regionName]
	if !ok {
		return nil, fmt.Errorf("region %s not found in resource spec", regionName)
	}

	if !regionTarget.Enabled {
		return nil, fmt.Errorf("region %s is not enabled", regionName)
	}

	// build health check if path provided
	var healthCheck *deploymentv1.HealthCheckConfig
	if healthCheckPath != "" {
		healthCheck = &deploymentv1.HealthCheckConfig{
			Path:                healthCheckPath,
			InitialDelaySeconds: 0, // could come from config later
		}
	}

	// build source (image type)
	buildSource := &deploymentv1.BuildSource{
		Type:  "image",
		Image: imageName,
	}

	// build env: inject region
	finalEnv := make(map[string]string)
	finalEnv["LOCO_REGION"] = regionName

	// construct immutable snapshot
	deploySpec := &deploymentv1.DeploymentSpec{
		Build:       buildSource,
		HealthCheck: healthCheck,

		// snapshot from RegionTarget
		Cpu:         regionTarget.Cpu,
		Memory:      regionTarget.Memory,
		MinReplicas: &regionTarget.MinReplicas,
		MaxReplicas: &regionTarget.MaxReplicas,
		TargetCpu:   regionTarget.TargetCpu,

		// env for controller (stripped before DB persistence)
		Env: finalEnv,

		// networking
		Port: resourceSpec.Routing.Port,
	}

	return deploySpec, nil
}

// configToResourceSpecV1 converts LocoConfig to ResourceSpec v0.1
func configToResourceSpecV1(cfg *LocoConfig) (*resourcev1.ResourceSpec, error) {
	// extract type from metadata
	resourceType := cfg.Metadata.Type
	if resourceType == "" {
		resourceType = "SERVICE"
	}

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

		if resourceCfg.CPUTarget > 0 {
			target.TargetCpu = &resourceCfg.CPUTarget
		}

		regions[regionName] = target
		firstRegion = false
	}

	spec := &resourcev1.ResourceSpec{
		Type:          resourceType,
		Routing:       routing,
		Observability: observability,
		Regions:       regions,
	}

	return spec, nil
}
