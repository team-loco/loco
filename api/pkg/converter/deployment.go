package converter

import (
	"fmt"

	deploymentv1 "github.com/loco-team/loco/shared/proto/deployment/v1"
	resourcev1 "github.com/loco-team/loco/shared/proto/resource/v1"
	locoControllerV1 "github.com/team-loco/loco/controller/api/v1alpha1"
	"google.golang.org/protobuf/encoding/protojson"
)

// DeserializeResourceSpec deserializes a ResourceSpec from JSON bytes (as stored in DB).
func DeserializeResourceSpec(specBytes []byte) (*resourcev1.ResourceSpec, error) {
	if len(specBytes) == 0 {
		return nil, fmt.Errorf("spec bytes cannot be empty")
	}

	var spec resourcev1.ResourceSpec
	err := protojson.Unmarshal(specBytes, &spec)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal resource spec: %w", err)
	}

	return &spec, nil
}

// MergeDeploymentSpec merges a request DeploymentSpec with resource defaults from ResourceSpec.
// The request spec takes precedence; missing fields are filled from the resource's primary region.
// This is the API's single source of truth for deployment defaults.
func MergeDeploymentSpec(
	resourceSpec *resourcev1.ResourceSpec,
	requestSpec *deploymentv1.DeploymentSpec,
) (*deploymentv1.DeploymentSpec, error) {
	if resourceSpec == nil {
		return nil, fmt.Errorf("resourceSpec cannot be nil")
	}
	if requestSpec == nil {
		return nil, fmt.Errorf("requestSpec cannot be nil")
	}

	// extract ServiceSpec from resourceSpec oneof
	resourceServiceSpec := resourceSpec.GetService()
	if resourceServiceSpec == nil {
		return nil, fmt.Errorf("resourceSpec must contain a service spec")
	}

	// extract ServiceDeploymentSpec from requestSpec oneof
	requestServiceSpec := requestSpec.GetService()
	if requestServiceSpec == nil {
		return nil, fmt.Errorf("deployment spec must contain a service spec")
	}

	// find requested region
	if requestServiceSpec.GetRegion() == "" {
		return nil, fmt.Errorf("region is required in deployment spec")
	}

	regionTarget, ok := resourceServiceSpec.Regions[requestServiceSpec.GetRegion()]
	if !ok {
		return nil, fmt.Errorf("region %s not found in resource spec", requestServiceSpec.GetRegion())
	}
	if !regionTarget.Enabled {
		return nil, fmt.Errorf("region %s is not enabled", requestServiceSpec.GetRegion())
	}

	// merge build (from request, always required)
	mergedServiceSpec := &deploymentv1.ServiceDeploymentSpec{
		Build:  requestServiceSpec.Build,
		Port:   requestServiceSpec.Port,
		Env:    requestServiceSpec.Env,
		Region: requestServiceSpec.Region,
	}

	// merge CPU (request > resource default)
	if requestServiceSpec.GetCpu() != "" {
		mergedServiceSpec.Cpu = requestServiceSpec.Cpu
	} else {
		mergedServiceSpec.Cpu = &regionTarget.Cpu
	}

	// merge Memory (request > resource default)
	if requestServiceSpec.GetMemory() != "" {
		mergedServiceSpec.Memory = requestServiceSpec.Memory
	} else {
		mergedServiceSpec.Memory = &regionTarget.Memory
	}

	// merge MinReplicas (request > resource default)
	if requestServiceSpec.GetMinReplicas() != 0 {
		mergedServiceSpec.MinReplicas = requestServiceSpec.MinReplicas
	} else {
		mergedServiceSpec.MinReplicas = &regionTarget.MinReplicas
	}

	// merge MaxReplicas (request > resource default)
	if requestServiceSpec.GetMaxReplicas() != 0 {
		mergedServiceSpec.MaxReplicas = requestServiceSpec.MaxReplicas
	} else {
		mergedServiceSpec.MaxReplicas = &regionTarget.MaxReplicas
	}

	// merge Scalers (request > resource default)
	if requestServiceSpec.GetScalers() != nil {
		mergedServiceSpec.Scalers = requestServiceSpec.Scalers
	} else if regionTarget.Scalers != nil {
		mergedServiceSpec.Scalers = regionTarget.Scalers
	}

	// merge HealthCheck (request > resource default)
	if requestServiceSpec.GetHealthCheck() != nil {
		mergedServiceSpec.HealthCheck = requestServiceSpec.HealthCheck
	} else if resourceServiceSpec.GetHealthCheck() != nil {
		mergedServiceSpec.HealthCheck = resourceServiceSpec.HealthCheck
	}

	// wrap merged ServiceDeploymentSpec in DeploymentSpec oneof
	mergedSpec := &deploymentv1.DeploymentSpec{
		Spec: &deploymentv1.DeploymentSpec_Service{
			Service: mergedServiceSpec,
		},
	}

	return mergedSpec, nil
}

// ProtoToServiceDeploymentSpec converts a proto DeploymentSpec to a controller ServiceDeploymentSpec
// This is the canonical conversion from proto (source of truth) to controller CRD types
func ProtoToServiceDeploymentSpec(spec *deploymentv1.DeploymentSpec) *locoControllerV1.ServiceDeploymentSpec {
	if spec == nil {
		return &locoControllerV1.ServiceDeploymentSpec{}
	}

	// extract ServiceDeploymentSpec from oneof
	serviceSpec := spec.GetService()
	if serviceSpec == nil {
		return &locoControllerV1.ServiceDeploymentSpec{}
	}

	var scalers *locoControllerV1.ScalersSpec
	if serviceSpec.GetScalers() != nil {
		scalers = &locoControllerV1.ScalersSpec{
			Enabled:      serviceSpec.GetScalers().GetEnabled(),
			CPUTarget:    serviceSpec.GetScalers().GetCpuTarget(),
			MemoryTarget: serviceSpec.GetScalers().GetMemoryTarget(),
		}
	}

	var healthCheck *locoControllerV1.HealthCheckSpec
	if serviceSpec.GetHealthCheck() != nil {
		healthCheck = &locoControllerV1.HealthCheckSpec{
			Path:               serviceSpec.GetHealthCheck().GetPath(),
			StartupGracePeriod: serviceSpec.GetHealthCheck().GetInitialDelaySeconds(),
			Interval:           serviceSpec.GetHealthCheck().GetIntervalSeconds(),
			Timeout:            serviceSpec.GetHealthCheck().GetTimeoutSeconds(),
			FailThreshold:      serviceSpec.GetHealthCheck().GetFailureThreshold(),
		}
	}

	return &locoControllerV1.ServiceDeploymentSpec{
		Image:          serviceSpec.GetBuild().GetImage(),
		Port:           serviceSpec.GetPort(),
		DockerfilePath: serviceSpec.GetBuild().GetDockerfilePath(),
		BuildType:      serviceSpec.GetBuild().GetType(),
		CPU:            serviceSpec.GetCpu(),
		Memory:         serviceSpec.GetMemory(),
		MinReplicas:    serviceSpec.GetMinReplicas(),
		MaxReplicas:    serviceSpec.GetMaxReplicas(),
		Scalers:        scalers,
		HealthCheck:    healthCheck,
		Env:            serviceSpec.GetEnv(),
	}
}

// ProtoToObsSpec converts a proto ObservabilityConfig to a controller ObsSpec
func ProtoToObsSpec(obs *resourcev1.ObservabilityConfig) *locoControllerV1.ObsSpec {
	if obs == nil {
		return nil
	}

	var logging locoControllerV1.LoggingSpec
	if obs.GetLogging() != nil {
		logging = locoControllerV1.LoggingSpec{
			Enabled:         obs.GetLogging().GetEnabled(),
			RetentionPeriod: obs.GetLogging().GetRetentionPeriod(),
			Structured:      obs.GetLogging().GetStructured(),
		}
	}

	var metrics locoControllerV1.MetricsSpec
	if obs.GetMetrics() != nil {
		metrics = locoControllerV1.MetricsSpec{
			Enabled: obs.GetMetrics().GetEnabled(),
			Path:    obs.GetMetrics().GetPath(),
			Port:    obs.GetMetrics().GetPort(),
		}
	}

	var tracing locoControllerV1.TracingSpec
	if obs.GetTracing() != nil {
		tracing = locoControllerV1.TracingSpec{
			Enabled:    obs.GetTracing().GetEnabled(),
			SampleRate: fmt.Sprintf("%v", obs.GetTracing().GetSampleRate()),
			Tags:       obs.GetTracing().GetTags(),
		}
	}

	return &locoControllerV1.ObsSpec{
		Logging: logging,
		Metrics: metrics,
		Tracing: tracing,
	}
}

func ProtoToRoutingSpec(routing *resourcev1.RoutingConfig, hostname string) *locoControllerV1.RoutingSpec {
	if routing == nil {
		return nil
	}

	return &locoControllerV1.RoutingSpec{
		HostName:    hostname,
		PathPrefix:  routing.GetPathPrefix(),
		IdleTimeout: routing.GetIdleTimeout(),
	}
}
