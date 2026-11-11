package converter

import (
	"errors"
	"fmt"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	ErrRegionNotFound           = errors.New("region not found in resource spec")
	ErrRegionDisabled           = errors.New("region is not enabled")
	errResourceSpecNotService   = errors.New("resourceSpec must contain a service spec")
	errDeploymentSpecNotService = errors.New("deployment spec must contain a service spec")
	errEmptySpecBytes           = errors.New("spec bytes cannot be empty")
)

// DeserializeResourceSpec deserializes a ResourceSpec from the JSON bytes stored in the database.
// Only services have a spec; any other type is an error.
func DeserializeResourceSpec(specBytes []byte, resourceType genDb.ResourceType) (*resourcev1.ResourceSpec, error) {
	if len(specBytes) == 0 {
		return nil, errEmptySpecBytes
	}

	switch resourceType {
	case genDb.ResourceTypeService:
		var serviceSpec resourcev1.ServiceSpec
		if err := protojson.Unmarshal(specBytes, &serviceSpec); err != nil {
			return nil, fmt.Errorf("failed to unmarshal service spec: %w", err)
		}
		return &resourcev1.ResourceSpec{
			Spec: &resourcev1.ResourceSpec_Service{Service: &serviceSpec},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported resource type: %s", resourceType)
	}
}

// DeserializeDeploymentSpec deserializes a DeploymentSpec from JSON bytes based on the resource type.
// The specBytes should contain only the inner deployment spec (ServiceDeploymentSpec, etc.), not the wrapper.
func DeserializeDeploymentSpec(
	specBytes []byte,
	resourceType genDb.ResourceType,
) (*deploymentv1.DeploymentSpec, error) {
	if len(specBytes) == 0 {
		return nil, errEmptySpecBytes
	}

	switch resourceType {
	case genDb.ResourceTypeService:
		var serviceSpec deploymentv1.ServiceDeploymentSpec
		if err := protojson.Unmarshal(specBytes, &serviceSpec); err != nil {
			return nil, fmt.Errorf("failed to unmarshal service deployment spec: %w", err)
		}
		return &deploymentv1.DeploymentSpec{
			Spec: &deploymentv1.DeploymentSpec_Service{Service: &serviceSpec},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported resource type: %s", resourceType)
	}
}

// MergeDeploymentSpec merges a request DeploymentSpec with the resource's region target and the
// API's configured defaults. The request takes precedence, then the region target, then the defaults.
func MergeDeploymentSpec(
	resourceSpec *resourcev1.ResourceSpec,
	requestSpec *deploymentv1.DeploymentSpec,
	region string,
	defaults servicedefaults.Defaults,
) (*deploymentv1.DeploymentSpec, error) {
	resourceServiceSpec := resourceSpec.GetService()
	if resourceServiceSpec == nil {
		return nil, errResourceSpecNotService
	}

	requestServiceSpec := requestSpec.GetService()
	if requestServiceSpec == nil {
		return nil, errDeploymentSpecNotService
	}

	regionTarget, ok := resourceServiceSpec.GetRegions()[region]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrRegionNotFound, region)
	}
	if !regionTarget.GetEnabled() {
		return nil, fmt.Errorf("%w: %s", ErrRegionDisabled, region)
	}

	resources := defaults.Fill(regionTarget, requestServiceSpec)
	healthCheck := requestServiceSpec.GetHealthCheck()
	if healthCheck == nil {
		healthCheck = resourceServiceSpec.GetHealthCheck()
	}
	mergedServiceSpec := &deploymentv1.ServiceDeploymentSpec{
		Metrics:     requestServiceSpec.GetMetrics(),
		RunAsUser:   requestServiceSpec.RunAsUser,
		RunAsGroup:  requestServiceSpec.RunAsGroup,
		Build:       requestServiceSpec.GetBuild(),
		Port:        requestServiceSpec.GetPort(),
		Env:         requestServiceSpec.GetEnv(),
		Cpu:         &resources.CPU,
		Memory:      &resources.Memory,
		MinReplicas: &resources.MinReplicas,
		MaxReplicas: &resources.MaxReplicas,
		Scalers:     resources.Scalers,
		HealthCheck: healthCheck,
	}

	return &deploymentv1.DeploymentSpec{
		Spec: &deploymentv1.DeploymentSpec_Service{Service: mergedServiceSpec},
	}, nil
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
		RunAsUser:   serviceSpec.RunAsUser,
		RunAsGroup:  serviceSpec.RunAsGroup,
		Image:       serviceSpec.GetBuild().GetImage(),
		Port:        serviceSpec.GetPort(),
		HealthCheck: healthCheck,
		Env:         serviceSpec.GetEnv(),
	}
}

// ProtoToObsSpec converts a proto ObservabilityConfig to a controller ObsSpec
func ProtoToObsSpec(obs *resourcev1.ObservabilityConfig) *locoControllerV1.ObsSpec {
	if obs == nil {
		return nil
	}

	logging := obs.GetLogging()
	metrics := obs.GetMetrics()
	return &locoControllerV1.ObsSpec{
		Logging: locoControllerV1.LoggingSpec{
			Enabled:         logging.GetEnabled(),
			RetentionPeriod: logging.GetRetentionPeriod(),
			Structured:      logging.GetStructured(),
		},
		Metrics: locoControllerV1.MetricsSpec{
			Enabled: metrics.GetEnabled(),
			Path:    metrics.GetPath(),
			Port:    metrics.GetPort(),
		},
	}
}

func ProtoToRoutingSpec(
	routing *resourcev1.RoutingConfig,
	hostname string,
	defaults servicedefaults.Defaults,
) *locoControllerV1.RoutingSpec {
	if hostname == "" {
		return nil
	}

	pathPrefix, idleTimeout := defaults.Routing(routing.GetPathPrefix(), routing.GetIdleTimeout())
	return &locoControllerV1.RoutingSpec{
		HostName:    hostname,
		PathPrefix:  pathPrefix,
		IdleTimeout: idleTimeout,
	}
}

func ProtoToResourcesSpec(service *deploymentv1.ServiceDeploymentSpec) *locoControllerV1.ResourcesSpec {
	scalers := service.GetScalers()
	return &locoControllerV1.ResourcesSpec{
		CPU:      service.GetCpu(),
		Memory:   service.GetMemory(),
		Replicas: locoControllerV1.ReplicasSpec{Min: service.GetMinReplicas(), Max: service.GetMaxReplicas()},
		Scalers: locoControllerV1.ScalersSpec{
			Enabled:      scalers.GetEnabled(),
			CPUTarget:    scalers.GetCpuTarget(),
			MemoryTarget: scalers.GetMemoryTarget(),
		},
	}
}
