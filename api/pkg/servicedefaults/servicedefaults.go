package servicedefaults

import (
	"errors"
	"fmt"

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
}

// Validate checks the defaults with the rules the controller applies to an Application, so
// the API cannot start with defaults that no deployment could use.
func (d Defaults) Validate() error {
	if err := locoControllerV1.ValidatePort(d.Port); err != nil {
		return fmt.Errorf("%w: %w", errInvalidPort, err)
	}
	health := d.HealthCheck()
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
	return nil
}

// HealthCheck is the default health check in the controller's form.
func (d Defaults) HealthCheck() locoControllerV1.HealthCheckSpec {
	return locoControllerV1.HealthCheckSpec{
		Path:               d.HealthPath,
		Interval:           d.HealthInterval,
		Timeout:            d.HealthTimeout,
		FailThreshold:      d.HealthFailThreshold,
		StartupGracePeriod: d.HealthStartupGracePeriod,
	}
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
