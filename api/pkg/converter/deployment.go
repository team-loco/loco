package converter

import (
	deploymentv1 "github.com/loco-team/loco/shared/proto/deployment/v1"
	locoControllerV1 "github.com/team-loco/loco/controller/api/v1alpha1"
)

// ProtoToDeploymentSpec converts a proto DeploymentSpec to a controller DeploymentSpec
// This is the canonical conversion from proto (source of truth) to controller CRD types
func ProtoToDeploymentSpec(spec *deploymentv1.DeploymentSpec, createdBy int64) *locoControllerV1.DeploymentSpec {
	if spec == nil {
		return &locoControllerV1.DeploymentSpec{}
	}

	result := &locoControllerV1.DeploymentSpec{
		Image: spec.Build.Image,
		Port: spec.Port,
		Env:  spec.Env,
	}

	// convert health check
	if spec.HealthCheck != nil {
		result.HealthCheck = &locoControllerV1.HealthCheckSpec{
			Path:               spec.HealthCheck.Path,
			StartupGracePeriod: spec.HealthCheck.InitialDelaySeconds,
			Interval:           spec.HealthCheck.IntervalSeconds,
			Timeout:            spec.HealthCheck.TimeoutSeconds,
		}
	}

	// convert build config
	if spec.Build != nil {
		if spec.Build.DockerfilePath != nil {
			result.DockerfilePath = *spec.Build.DockerfilePath
		}
		result.BuildType = spec.Build.Type
	}

	return result
}
