package v1alpha1

import (
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

var (
	dockerImagePattern = regexp.MustCompile(`^([a-z0-9\-._]+(/[a-z0-9\-._]+)*)(:[a-z0-9\-._]+|@sha256:[a-f0-9]{64})?$`)
	envVarNamePattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// ValidateApplicationSpec validates the entire ApplicationSpec
func (spec *ApplicationSpec) Validate() error {
	if spec == nil {
		return fmt.Errorf("applicationSpec cannot be nil")
	}

	if spec.ResourceId == 0 {
		return fmt.Errorf("resourceId must be set")
	}

	if spec.WorkspaceId == 0 {
		return fmt.Errorf("workspaceId must be set")
	}

	if spec.Type == "" {
		return fmt.Errorf("type must be set")
	}

	switch spec.Type {
	case "SERVICE":
		if spec.ServiceSpec == nil {
			return fmt.Errorf("serviceSpec must be set for SERVICE type")
		}
		return validateServiceSpec(spec.ServiceSpec)
	case "DATABASE":
		return fmt.Errorf("database resource type validation: TODO")
	case "CACHE":
		return fmt.Errorf("cache resource type validation: TODO")
	case "QUEUE":
		return fmt.Errorf("queue resource type validation: TODO")
	case "BLOB":
		return fmt.Errorf("blob resource type validation: TODO")
	default:
		return fmt.Errorf("unknown resource type: %s", spec.Type)
	}
}

// validateServiceSpec validates the ServiceSpec
func validateServiceSpec(spec *ServiceSpec) error {
	if spec == nil {
		return fmt.Errorf("serviceSpec cannot be nil")
	}

	if spec.Deployment == nil {
		return fmt.Errorf("serviceSpec.deployment must be set")
	}

	if err := validateServiceDeploymentSpec(spec.Deployment); err != nil {
		return fmt.Errorf("invalid deployment: %w", err)
	}

	if spec.Resources != nil {
		if err := validateResourcesSpec(spec.Resources); err != nil {
			return fmt.Errorf("invalid resources: %w", err)
		}
	}

	if spec.Obs != nil {
		if err := validateObsSpec(spec.Obs); err != nil {
			return fmt.Errorf("invalid observability: %w", err)
		}
	}

	if spec.Routing != nil {
		if err := validateRoutingSpec(spec.Routing); err != nil {
			return fmt.Errorf("invalid routing: %w", err)
		}
	}

	return nil
}

// validateServiceDeploymentSpec validates the ServiceDeploymentSpec
func validateServiceDeploymentSpec(spec *ServiceDeploymentSpec) error {
	if spec == nil {
		return fmt.Errorf("deployment cannot be nil")
	}

	// Image validation (required)
	if spec.Image == "" {
		return fmt.Errorf("image must be set")
	}
	if !dockerImagePattern.MatchString(spec.Image) {
		return fmt.Errorf("image format invalid: %q (must include registry, image name, and tag/digest)", spec.Image)
	}
	if !strings.Contains(spec.Image, ":") && !strings.Contains(spec.Image, "@") {
		return fmt.Errorf("image %q must include a tag (e.g., :v1.0) or digest (e.g., @sha256:...)", spec.Image)
	}

	// Port validation (required)
	if spec.Port < 1024 || spec.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", spec.Port)
	}

	// HealthCheck validation (optional)
	if spec.HealthCheck != nil {
		if err := validateHealthCheckSpec(spec.HealthCheck); err != nil {
			return err
		}
	}

	// Env validation
	if len(spec.Env) > 100 {
		return fmt.Errorf("too many environment variables: %d (max 100)", len(spec.Env))
	}
	for name, value := range spec.Env {
		if !envVarNamePattern.MatchString(name) {
			return fmt.Errorf("invalid environment variable name %q (must start with letter or underscore, contain only alphanumeric and underscore)", name)
		}
		if value == "" {
			return fmt.Errorf("environment variable %q has empty value", name)
		}
	}

	return nil
}

// validateCPUQuantity validates CPU format (100m - 2000m)
func validateCPUQuantity(cpu string) error {
	qty, err := resource.ParseQuantity(cpu)
	if err != nil {
		return fmt.Errorf("invalid CPU format: %s", cpu)
	}

	// Check bounds: 100m - 2000m
	minCPU := resource.NewMilliQuantity(100, resource.DecimalSI)
	maxCPU := resource.NewMilliQuantity(2000, resource.DecimalSI)

	if qty.Cmp(*minCPU) < 0 {
		return fmt.Errorf("CPU %s is below minimum (100m)", cpu)
	}
	if qty.Cmp(*maxCPU) > 0 {
		return fmt.Errorf("CPU %s exceeds maximum (2000m)", cpu)
	}

	return nil
}

// validateMemoryQuantity validates Memory format (32Mi - 4Gi)
func validateMemoryQuantity(memory string) error {
	qty, err := resource.ParseQuantity(memory)
	if err != nil {
		return fmt.Errorf("invalid memory format: %s", memory)
	}

	// Check bounds: 32Mi - 4Gi
	minMem := resource.NewQuantity(32*1024*1024, resource.BinarySI)
	maxMem := resource.NewQuantity(4*1024*1024*1024, resource.BinarySI)

	if qty.Cmp(*minMem) < 0 {
		return fmt.Errorf("memory %s is below minimum (32Mi)", memory)
	}
	if qty.Cmp(*maxMem) > 0 {
		return fmt.Errorf("memory %s exceeds maximum (4Gi)", memory)
	}

	return nil
}

// validateHealthCheckSpec validates the HealthCheckSpec (optional)
func validateHealthCheckSpec(spec *HealthCheckSpec) error {
	if spec == nil {
		return nil // optional
	}

	// Path validation
	if spec.Path == "" {
		return fmt.Errorf("healthCheck.path must be set")
	}
	if !strings.HasPrefix(spec.Path, "/") {
		return fmt.Errorf("healthCheck.path must start with '/'")
	}

	// Startup grace period (max 3 minutes = 180 seconds)
	if spec.StartupGracePeriod > 180 {
		return fmt.Errorf("healthCheck.startupGracePeriod cannot exceed 180 seconds (3 minutes), got %d", spec.StartupGracePeriod)
	}

	// Interval (min 5 seconds)
	if spec.Interval < 5 {
		return fmt.Errorf("healthCheck.interval must be at least 5 seconds, got %d", spec.Interval)
	}

	// Timeout (max 1 minute = 60 seconds)
	if spec.Timeout > 60 {
		return fmt.Errorf("healthCheck.timeout cannot exceed 60 seconds, got %d", spec.Timeout)
	}

	// FailThreshold (1-10)
	if spec.FailThreshold < 1 || spec.FailThreshold > 10 {
		return fmt.Errorf("healthCheck.failThreshold must be between 1 and 10, got %d", spec.FailThreshold)
	}

	return nil
}

// validateResourcesSpec validates the ResourcesSpec
func validateResourcesSpec(spec *ResourcesSpec) error {
	if spec == nil {
		return nil
	}

	// CPU validation
	if spec.CPU != "" {
		if err := validateCPUQuantity(spec.CPU); err != nil {
			return fmt.Errorf("cpu: %w", err)
		}
	}

	// Memory validation
	if spec.Memory != "" {
		if err := validateMemoryQuantity(spec.Memory); err != nil {
			return fmt.Errorf("memory: %w", err)
		}
	}

	// Replicas validation
	if spec.Replicas.Min < 1 {
		return fmt.Errorf("replicas.min must be at least 1, got %d", spec.Replicas.Min)
	}
	if spec.Replicas.Max > 10 {
		return fmt.Errorf("replicas.max cannot exceed 10, got %d", spec.Replicas.Max)
	}
	if spec.Replicas.Max > 0 && spec.Replicas.Min > 0 && spec.Replicas.Max < spec.Replicas.Min {
		return fmt.Errorf("replicas.max (%d) must be >= replicas.min (%d)", spec.Replicas.Max, spec.Replicas.Min)
	}

	// Scalers validation
	if spec.Scalers.Enabled {
		if err := validateScalersSpec(&spec.Scalers); err != nil {
			return fmt.Errorf("scalers: %w", err)
		}
	}

	return nil
}

// validateScalersSpec validates the ScalersSpec
func validateScalersSpec(spec *ScalersSpec) error {
	if spec == nil {
		return nil
	}

	if !spec.Enabled {
		return nil
	}

	// CPU target (1-100%)
	if spec.CPUTarget != 0 && (spec.CPUTarget < 1 || spec.CPUTarget > 100) {
		return fmt.Errorf("cpuTarget must be between 1 and 100, got %d", spec.CPUTarget)
	}

	// Memory target (1-100%)
	if spec.MemoryTarget != 0 && (spec.MemoryTarget < 1 || spec.MemoryTarget > 100) {
		return fmt.Errorf("memoryTarget must be between 1 and 100, got %d", spec.MemoryTarget)
	}

	return nil
}

// validateObsSpec validates the ObsSpec
func validateObsSpec(spec *ObsSpec) error {
	if spec == nil {
		return nil
	}

	// Logging validation (optional)
	if !spec.Logging.Enabled {
		// no additional validation needed when disabled
	}

	// Metrics validation (optional)
	if spec.Metrics.Enabled {
		if spec.Metrics.Path != "" && !strings.HasPrefix(spec.Metrics.Path, "/") {
			return fmt.Errorf("metrics.path must start with '/'")
		}
		if spec.Metrics.Port < 1024 || spec.Metrics.Port > 65535 {
			return fmt.Errorf("metrics.port must be between 1 and 65535, got %d", spec.Metrics.Port)
		}
	}

	// Tracing validation (optional)
	if spec.Tracing.Enabled {
		// sample rate validation could be added if needed
	}

	return nil
}

// validateRoutingSpec validates the RoutingSpec
func validateRoutingSpec(spec *RoutingSpec) error {
	if spec == nil {
		return nil
	}

	if spec.HostName == "" {
		return fmt.Errorf("routing.hostname must be set")
	}

	if spec.PathPrefix != "" && !strings.HasPrefix(spec.PathPrefix, "/") {
		return fmt.Errorf("routing.pathPrefix must start with '/'")
	}

	if spec.IdleTimeout < 0 {
		return fmt.Errorf("routing.idleTimeout cannot be negative")
	}

	return nil
}
