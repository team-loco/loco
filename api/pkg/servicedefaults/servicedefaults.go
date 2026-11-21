package servicedefaults

import (
	"errors"
	"fmt"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/workloadplatform"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
)

var (
	ErrInvalidCPU       = errors.New("invalid CPU quantity")
	ErrInvalidMemory    = errors.New("invalid memory quantity")
	errInvalidResources = errors.New("the default resources are not accepted by the controller")
	errInvalidRoute     = errors.New("the default route is not accepted by the controller")
	errInvalidHealth    = errors.New("the default health check is not accepted by the controller")
	errInvalidRestarts  = errors.New("the default failure policy is not accepted by the controller")
)

// Defaults carries installation service policy and the values the API fills when a request omits them.
// There is no default port: a service without one is a worker.
type Defaults struct {
	TargetPlatform           workloadplatform.Platform
	CPU                      string
	Memory                   string
	MinReplicas              int32
	MaxReplicas              int32
	CPUTarget                int32
	PathPrefix               string
	IdleTimeout              int32
	HealthPath               string
	HealthInterval           int32
	HealthTimeout            int32
	HealthFailThreshold      int32
	HealthStartupGracePeriod int32
	MaxRestarts              int32
	DeployTimeout            int32
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
func (d Defaults) Fill(region *resourcev1.RegionTarget, request *deploymentv1.RegionDeploymentTarget) Resources {
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

// Proto is the region sizing a deployment stores.
func (r Resources) Proto() *deploymentv1.RegionDeploymentTarget {
	return &deploymentv1.RegionDeploymentTarget{
		Cpu:         &r.CPU,
		Memory:      &r.Memory,
		MinReplicas: &r.MinReplicas,
		MaxReplicas: &r.MaxReplicas,
		Scalers:     r.Scalers,
	}
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

// FailurePolicy is when Loco stops a version of a service.
type FailurePolicy struct {
	Restart              locoControllerV1.RestartPolicySpec
	DeployTimeoutSeconds int32
}

// FailurePolicy is a service's failure policy: what the service sets, then the defaults. The
// default restart mode is on-failure, and only on-failure takes a budget.
func (d Defaults) FailurePolicy(mode *string, maxRestarts, deployTimeout *int32) FailurePolicy {
	policy := FailurePolicy{
		Restart:              locoControllerV1.RestartPolicySpec{Mode: locoControllerV1.RestartOnFailure},
		DeployTimeoutSeconds: d.DeployTimeout,
	}
	if mode != nil {
		policy.Restart.Mode = locoControllerV1.RestartMode(*mode)
	}
	if policy.Restart.Mode == locoControllerV1.RestartOnFailure {
		policy.Restart.MaxRestarts = d.MaxRestarts
		if maxRestarts != nil {
			policy.Restart.MaxRestarts = *maxRestarts
		}
	}
	if deployTimeout != nil {
		policy.DeployTimeoutSeconds = *deployTimeout
	}
	return policy
}

// Validate checks the policy with the rules the controller applies.
func (p FailurePolicy) Validate() error {
	if err := p.Restart.Validate(); err != nil {
		return err
	}
	return locoControllerV1.ValidateDeployTimeout(p.DeployTimeoutSeconds)
}

// Proto is the policy as the API reports it.
func (p FailurePolicy) Proto() *deploymentv1.FailurePolicy {
	return &deploymentv1.FailurePolicy{
		RestartPolicy:        RestartPolicyProto(p.Restart.Mode),
		MaxRestarts:          p.Restart.MaxRestarts,
		DeployTimeoutSeconds: p.DeployTimeoutSeconds,
	}
}

// RestartMode maps a proto restart policy to its mode; it is empty for an unset one.
func RestartMode(policy deploymentv1.RestartPolicy) locoControllerV1.RestartMode {
	for _, mode := range []locoControllerV1.RestartMode{
		locoControllerV1.RestartOnFailure, locoControllerV1.RestartAlways, locoControllerV1.RestartNever,
	} {
		if RestartPolicyProto(mode) == policy {
			return mode
		}
	}
	return ""
}

// RestartPolicyProto maps a restart mode to its proto value; agent/pkg/appwatch keeps the same
// mapping, update both together.
func RestartPolicyProto(mode locoControllerV1.RestartMode) deploymentv1.RestartPolicy {
	switch mode {
	case locoControllerV1.RestartOnFailure:
		return deploymentv1.RestartPolicy_RESTART_POLICY_ON_FAILURE
	case locoControllerV1.RestartAlways:
		return deploymentv1.RestartPolicy_RESTART_POLICY_ALWAYS
	case locoControllerV1.RestartNever:
		return deploymentv1.RestartPolicy_RESTART_POLICY_NEVER
	default:
		return deploymentv1.RestartPolicy_RESTART_POLICY_UNSPECIFIED
	}
}

// Validate checks the defaults with the rules the controller applies to an Application, so
// the API cannot start with defaults that no deployment could use.
func (d Defaults) Validate() error {
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
		CPU:     d.CPU,
		Memory:  d.Memory,
		Scalers: locoControllerV1.ScalersSpec{Enabled: true, CPUTarget: d.CPUTarget},
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
	if err := d.FailurePolicy(nil, nil, nil).Validate(); err != nil {
		return fmt.Errorf("%w: %w", errInvalidRestarts, err)
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
