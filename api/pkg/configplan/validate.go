package configplan

import (
	"maps"
	"slices"

	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// validateDesired checks a service as the apply would write it, with the defaults filled in,
// against the rules the controller applies to an Application, so a value no deployment could
// use is a plan error at its field path rather than a failure during the apply.
func validateDesired(name string, desired State) []Error {
	var errs []Error
	add := func(path string, err error) {
		if err != nil {
			errs = append(errs, Error{Service: name, Path: path, Err: err})
		}
	}

	add(pathPort, locoControllerV1.ValidatePort(desired.Port))
	health := locoControllerV1.HealthCheckSpec{
		Path:               desired.Health.Path,
		Interval:           desired.Health.Interval,
		Timeout:            desired.Health.Timeout,
		FailThreshold:      desired.Health.FailThreshold,
		StartupGracePeriod: desired.Health.StartupGracePeriod,
	}
	add(pathHealth, health.Validate())
	if desired.Routing != nil {
		add(pathRouting, locoControllerV1.ValidateRoute(desired.Routing.PathPrefix, desired.Routing.IdleTimeout))
	}
	add(pathEnv, locoControllerV1.ValidateEnv(desired.Env))
	for _, region := range slices.Sorted(maps.Keys(desired.Regions)) {
		target := desired.Regions[region]
		resources := locoControllerV1.ResourcesSpec{
			CPU:      target.CPU,
			Memory:   target.Memory,
			Replicas: locoControllerV1.ReplicasSpec{Min: target.MinReplicas, Max: target.MaxReplicas},
		}
		if target.Autoscaling != nil {
			resources.Scalers = locoControllerV1.ScalersSpec{
				Enabled:      true,
				CPUTarget:    target.Autoscaling.CPUTarget,
				MemoryTarget: target.Autoscaling.MemoryTarget,
			}
		}
		add(pathRegions+"."+region, resources.Validate())
	}
	return errs
}
