package servicedefaults

import (
	"errors"
	"fmt"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/locofile"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
)

var (
	ErrInvalidCPU       = errors.New("invalid CPU quantity")
	ErrInvalidMemory    = errors.New("invalid memory quantity")
	errInvalidResources = errors.New("the default resources are not accepted by the controller")
	errInvalidRoute     = errors.New("the default route is not accepted by the controller")
	errInvalidPort      = errors.New("the default port is not accepted by the controller")
	errInvalidHealth    = errors.New("the default health check is not accepted by the controller")
	errInvalidDeploy    = errors.New("the default deploy policy is outside the loco.yaml bounds")
)

// Defaults is what the API fills into a service when the file or request omits it.
type Defaults struct {
	CPU                      string
	Memory                   string
	MinReplicas              int32
	MaxReplicas              int32
	PathPrefix               string
	IdleTimeout              int32
	Port                     int32
	HealthPath               string
	HealthInterval           int32
	HealthTimeout            int32
	HealthFailThreshold      int32
	HealthStartupGracePeriod int32
	DeployAttempts           int32
	DeployRetryDelay         int32
	MetricsPort              int32
	MetricsPath              string
	LogRetention             string
}

// Resources is what a deployment runs with in one region.
type Resources struct {
	CPU         string
	Memory      string
	MinReplicas int32
	MaxReplicas int32
	Scalers     *deploymentv1.Scalers
}

// Fill returns the resources a deployment runs with: what the request sets, then what the
// region target sets, then the defaults. Without a maximum, the replicas never scale below
// the minimum.
func (d Defaults) Fill(region *resourcev1.RegionTarget, request *deploymentv1.ServiceDeploymentSpec) Resources {
	filled := Resources{
		CPU:         FirstSet(request.GetCpu(), region.GetCpu(), d.CPU),
		Memory:      FirstSet(request.GetMemory(), region.GetMemory(), d.Memory),
		MinReplicas: FirstSet(request.GetMinReplicas(), region.GetMinReplicas(), d.MinReplicas),
		MaxReplicas: FirstSet(request.GetMaxReplicas(), region.GetMaxReplicas()),
		Scalers:     request.GetScalers(),
	}
	if filled.MaxReplicas == 0 {
		filled.MaxReplicas = max(d.MaxReplicas, filled.MinReplicas)
	}
	if filled.Scalers == nil {
		filled.Scalers = region.GetScalers()
	}
	return filled
}

// Routing returns the path prefix and idle timeout of a route, with the defaults for the ones
// it leaves unset.
func (d Defaults) Routing(pathPrefix string, idleTimeout int32) (string, int32) {
	return FirstSet(pathPrefix, d.PathPrefix), FirstSet(idleTimeout, d.IdleTimeout)
}

// Health is the default health check.
func (d Defaults) Health() locoControllerV1.HealthCheckSpec {
	return locoControllerV1.HealthCheckSpec{
		Path:               d.HealthPath,
		Interval:           d.HealthInterval,
		Timeout:            d.HealthTimeout,
		FailThreshold:      d.HealthFailThreshold,
		StartupGracePeriod: d.HealthStartupGracePeriod,
	}
}

// Validate checks the defaults with the rules the controller applies to an Application, so
// the API cannot start with defaults that no deployment could use.
func (d Defaults) Validate() error {
	if err := locoControllerV1.ValidatePort(d.Port); err != nil {
		return fmt.Errorf("%w: %w", errInvalidPort, err)
	}
	health := d.Health()
	if err := health.Validate(); err != nil {
		return fmt.Errorf("%w: %w", errInvalidHealth, err)
	}
	if _, err := ParseCPU(d.CPU); err != nil {
		return err
	}
	if _, err := ParseMemory(d.Memory); err != nil {
		return err
	}
	resources := locoControllerV1.ResourcesSpec{
		CPU:    d.CPU,
		Memory: d.Memory,
		Replicas: locoControllerV1.ReplicasSpec{
			Min: d.MinReplicas,
			Max: d.MaxReplicas,
		},
	}
	if err := resources.Validate(); err != nil {
		return fmt.Errorf("%w: %w", errInvalidResources, err)
	}
	if err := locoControllerV1.ValidateRoute(d.PathPrefix, d.IdleTimeout); err != nil {
		return fmt.Errorf("%w: %w", errInvalidRoute, err)
	}
	if err := locofile.CheckDeployAttempts(d.DeployAttempts); err != nil {
		return fmt.Errorf("%w: attempts %w", errInvalidDeploy, err)
	}
	if err := locofile.CheckDeployRetryDelay(d.DeployRetryDelay); err != nil {
		return fmt.Errorf("%w: retry delay %w", errInvalidDeploy, err)
	}
	return nil
}

func ParseCPU(value string) (resource.Quantity, error) {
	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		return resource.Quantity{}, fmt.Errorf("%w %q: %w", ErrInvalidCPU, value, err)
	}
	return quantity, nil
}

func ParseMemory(value string) (resource.Quantity, error) {
	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		return resource.Quantity{}, fmt.Errorf("%w %q: %w", ErrInvalidMemory, value, err)
	}
	return quantity, nil
}

// FirstSet returns the first value that is not the zero value.
func FirstSet[T comparable](values ...T) T {
	var zero T
	for _, value := range values {
		if value != zero {
			return value
		}
	}
	return zero
}
