package v1alpha1

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

var (
	ErrAutoscalingUnsupported = errors.New("autoscaling is not supported yet")
	errResourceIDMissing      = errors.New("resourceId must be set")
	errWorkspaceIDMissing     = errors.New("workspaceId must be set")
	errTypeMissing            = errors.New("type must be set")
	errServiceSpecMissing     = errors.New("serviceSpec must be set for SERVICE type")
	errDatabaseValidationTODO = errors.New("database resource type validation: TODO")
	errCacheValidationTODO    = errors.New("cache resource type validation: TODO")
	errQueueValidationTODO    = errors.New("queue resource type validation: TODO")
	errBlobValidationTODO     = errors.New("blob resource type validation: TODO")
	errDeploymentMissing      = errors.New("serviceSpec.deployment must be set")
	ErrRuntimeID              = errors.New("runtime user and group IDs must be between 1 and 2147483647")
	errImageMissing           = errors.New("image must be set")
	errHealthPathMissing      = errors.New("healthCheck.path must be set")
	errHealthPathNoSlash      = errors.New("healthCheck.path must start with '/'")
	errMetricsPath            = errors.New("metrics.path must be an absolute HTTP path without a query or fragment")
	errHostnameMissing        = errors.New("routing.hostname must be set")
	errPathPrefixNoSlash      = errors.New("routing.pathPrefix must start with '/'")
	errIdleTimeoutNotPositive = errors.New("routing.idleTimeout must be positive")
	errResourcesMissing       = errors.New("serviceSpec.resources must be set")
	errCPUMissing             = errors.New("cpu must be set")
	errMemoryMissing          = errors.New("memory must be set")
	errGraceNegative          = errors.New("healthCheck.startupGracePeriod cannot be negative")
	errEnvSecretNameMissing   = errors.New("envSecretRef.name must be set")
	errEnvSecretRevision      = errors.New("envSecretRef.revision must be positive")
)

// Size limits a ResourcesSpec must fall within. The API's GetConfig reports them so
// clients can offer only sizes that validate.
const (
	MinCPUMillis   = 100
	MaxCPUMillis   = 2000
	MinMemoryBytes = 32 * 1024 * 1024
	MaxMemoryBytes = 4 * 1024 * 1024 * 1024
	MinReplicas    = 1
	MaxReplicas    = 10
)

const (
	minServicePort         = 1024
	maxServicePort         = 65535
	maxEnvVars             = 100
	maxStartupGracePeriod  = 180
	minHealthInterval      = 5
	minHealthTimeout       = 1
	maxHealthTimeout       = 60
	minHealthFailThreshold = 1
	maxHealthFailThreshold = 10
)

var (
	dockerImagePattern = regexp.MustCompile(
		`^([a-z0-9\-._]+(:[0-9]+)?(/[a-z0-9\-._]+)*)(:[a-z0-9\-._]+)?(@sha256:[a-f0-9]{64})?$`,
	)
	envVarNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// ValidateApplicationSpec validates the entire ApplicationSpec
func (spec *ApplicationSpec) Validate() error {
	if spec.ResourceID == "" {
		return errResourceIDMissing
	}

	if spec.WorkspaceID == "" {
		return errWorkspaceIDMissing
	}

	if spec.Type == "" {
		return errTypeMissing
	}

	switch spec.Type {
	case "SERVICE":
		if spec.ServiceSpec == nil {
			return errServiceSpecMissing
		}
		return validateServiceSpec(spec.ServiceSpec)
	case "DATABASE":
		return errDatabaseValidationTODO
	case "CACHE":
		return errCacheValidationTODO
	case "QUEUE":
		return errQueueValidationTODO
	case "BLOB":
		return errBlobValidationTODO
	default:
		return fmt.Errorf("unknown resource type: %s", spec.Type)
	}
}

// validateServiceSpec validates the ServiceSpec
func validateServiceSpec(spec *ServiceSpec) error {
	if spec.Deployment == nil {
		return errDeploymentMissing
	}

	if err := validateServiceDeploymentSpec(spec.Deployment); err != nil {
		return fmt.Errorf("invalid deployment: %w", err)
	}

	if spec.Resources == nil {
		return errResourcesMissing
	}
	if err := spec.Resources.Validate(); err != nil {
		return fmt.Errorf("invalid resources: %w", err)
	}

	if spec.Obs != nil {
		if err := spec.Obs.Metrics.Validate(); err != nil {
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
	// Image validation (required)
	if spec.Image == "" {
		return errImageMissing
	}
	if !dockerImagePattern.MatchString(spec.Image) {
		return fmt.Errorf("image format invalid: %q (must include registry, image name, and tag/digest)", spec.Image)
	}
	if !strings.Contains(spec.Image, ":") && !strings.Contains(spec.Image, "@") {
		return fmt.Errorf("image %q must include a tag (e.g., :v1.0) or digest (e.g., @sha256:...)", spec.Image)
	}

	if err := ValidateRuntimeID(spec.RunAsUser); err != nil {
		return err
	}
	if err := ValidateRuntimeID(spec.RunAsGroup); err != nil {
		return err
	}

	if err := ValidatePort(spec.Port); err != nil {
		return err
	}

	if spec.HealthCheck != nil {
		if err := spec.HealthCheck.Validate(); err != nil {
			return err
		}
	}

	if err := ValidateEnv(spec.Env); err != nil {
		return err
	}

	if spec.EnvSecretRef != nil {
		return validateEnvSecretRef(spec.EnvSecretRef)
	}
	return nil
}

// ValidatePort checks the container port a service listens on.
func ValidatePort(port int32) error {
	if port < minServicePort || port > maxServicePort {
		return fmt.Errorf("port must be between %d and %d, got %d", minServicePort, maxServicePort, port)
	}
	return nil
}

// ValidateEnv checks the names, values and count of a service's environment variables.
func ValidateEnv(env map[string]string) error {
	if len(env) > maxEnvVars {
		return fmt.Errorf("too many environment variables: %d (max %d)", len(env), maxEnvVars)
	}
	for name, value := range env {
		if !envVarNamePattern.MatchString(name) {
			return fmt.Errorf(
				"invalid environment variable name %q "+
					"(must start with letter or underscore, contain only alphanumeric and underscore)",
				name,
			)
		}
		if value == "" {
			return fmt.Errorf("environment variable %q has empty value", name)
		}
	}
	return nil
}

func validateEnvSecretRef(ref *EnvSecretRef) error {
	if ref.Name == "" {
		return errEnvSecretNameMissing
	}
	if ref.Revision < 1 {
		return errEnvSecretRevision
	}
	return nil
}

// validateCPUQuantity validates the CPU format and bounds.
func validateCPUQuantity(cpu string) error {
	qty, err := resource.ParseQuantity(cpu)
	if err != nil {
		return fmt.Errorf("invalid CPU format: %s", cpu)
	}

	minCPU := resource.NewMilliQuantity(MinCPUMillis, resource.DecimalSI)
	maxCPU := resource.NewMilliQuantity(MaxCPUMillis, resource.DecimalSI)

	if qty.Cmp(*minCPU) < 0 {
		return fmt.Errorf("CPU %s is below minimum (%dm)", cpu, MinCPUMillis)
	}
	if qty.Cmp(*maxCPU) > 0 {
		return fmt.Errorf("CPU %s exceeds maximum (%dm)", cpu, MaxCPUMillis)
	}

	return nil
}

// validateMemoryQuantity validates the memory format and bounds.
func validateMemoryQuantity(memory string) error {
	qty, err := resource.ParseQuantity(memory)
	if err != nil {
		return fmt.Errorf("invalid memory format: %s", memory)
	}

	minMem := resource.NewQuantity(MinMemoryBytes, resource.BinarySI)
	maxMem := resource.NewQuantity(MaxMemoryBytes, resource.BinarySI)

	if qty.Cmp(*minMem) < 0 {
		return fmt.Errorf("memory %s is below minimum (%s)", memory, minMem)
	}
	if qty.Cmp(*maxMem) > 0 {
		return fmt.Errorf("memory %s exceeds maximum (%s)", memory, maxMem)
	}

	return nil
}

// Validate checks a health check's path and timings.
func (spec *HealthCheckSpec) Validate() error {
	if spec.Path == "" {
		return errHealthPathMissing
	}
	if !strings.HasPrefix(spec.Path, "/") {
		return errHealthPathNoSlash
	}

	if spec.StartupGracePeriod < 0 {
		return errGraceNegative
	}
	if spec.StartupGracePeriod > maxStartupGracePeriod {
		return fmt.Errorf(
			"healthCheck.startupGracePeriod cannot exceed %d seconds, got %d",
			maxStartupGracePeriod,
			spec.StartupGracePeriod,
		)
	}

	if spec.Interval < minHealthInterval {
		return fmt.Errorf("healthCheck.interval must be at least %d seconds, got %d", minHealthInterval, spec.Interval)
	}

	if spec.Timeout < minHealthTimeout || spec.Timeout > maxHealthTimeout {
		return fmt.Errorf(
			"healthCheck.timeout must be between %d and %d seconds, got %d",
			minHealthTimeout,
			maxHealthTimeout,
			spec.Timeout,
		)
	}

	if spec.FailThreshold < minHealthFailThreshold || spec.FailThreshold > maxHealthFailThreshold {
		return fmt.Errorf(
			"healthCheck.failThreshold must be between %d and %d, got %d",
			minHealthFailThreshold,
			maxHealthFailThreshold,
			spec.FailThreshold,
		)
	}

	return nil
}

// Validate validates the ResourcesSpec
func (spec *ResourcesSpec) Validate() error {
	if spec.CPU == "" {
		return errCPUMissing
	}
	if err := validateCPUQuantity(spec.CPU); err != nil {
		return fmt.Errorf("cpu: %w", err)
	}

	if spec.Memory == "" {
		return errMemoryMissing
	}
	if err := validateMemoryQuantity(spec.Memory); err != nil {
		return fmt.Errorf("memory: %w", err)
	}

	// Replicas validation
	if spec.Replicas.Min < MinReplicas {
		return fmt.Errorf("replicas.min must be at least %d, got %d", MinReplicas, spec.Replicas.Min)
	}
	if spec.Replicas.Max > MaxReplicas {
		return fmt.Errorf("replicas.max cannot exceed %d, got %d", MaxReplicas, spec.Replicas.Max)
	}
	if spec.Replicas.Max < spec.Replicas.Min {
		return fmt.Errorf("replicas.max (%d) must be >= replicas.min (%d)", spec.Replicas.Max, spec.Replicas.Min)
	}

	if spec.Scalers.Enabled {
		return ErrAutoscalingUnsupported
	}
	return nil
}

// validateRoutingSpec validates the RoutingSpec
func validateRoutingSpec(spec *RoutingSpec) error {
	if spec.HostName == "" {
		return errHostnameMissing
	}

	return ValidateRoute(spec.PathPrefix, spec.IdleTimeout)
}

// ValidateRoute checks the path prefix and idle timeout of a service's HTTP route.
func ValidateRoute(pathPrefix string, idleTimeout int32) error {
	if !strings.HasPrefix(pathPrefix, "/") {
		return errPathPrefixNoSlash
	}
	if idleTimeout < 1 {
		return errIdleTimeoutNotPositive
	}
	return nil
}

func ValidateRuntimeID(id *int64) error {
	if id != nil && (*id <= 0 || *id > math.MaxInt32) {
		return ErrRuntimeID
	}
	return nil
}

func (spec *MetricsSpec) Validate() error {
	if !spec.Enabled {
		return nil
	}
	parsed, err := url.ParseRequestURI(spec.Path)
	if err != nil || !strings.HasPrefix(spec.Path, "/") || strings.HasPrefix(spec.Path, "//") ||
		parsed.RawQuery != "" ||
		strings.ContainsAny(spec.Path, "#\r\n") {
		return errMetricsPath
	}

	if spec.Port < minServicePort || spec.Port > maxServicePort {
		return fmt.Errorf("metrics.port must be between %d and %d, got %d", minServicePort, maxServicePort, spec.Port)
	}
	return nil
}
