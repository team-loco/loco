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
	errNilResourceSpec          = errors.New("resourceSpec cannot be nil")
	errNilRequestSpec           = errors.New("requestSpec cannot be nil")
	errRegionRequired           = errors.New("region is required")
	ErrRegionNotFound           = errors.New("region not found in resource spec")
	ErrRegionDisabled           = errors.New("region is not enabled")
	errResourceSpecNotService   = errors.New("resourceSpec must contain a service spec")
	errDeploymentSpecNotService = errors.New("deployment spec must contain a service spec")
	errEmptySpecBytes           = errors.New("spec bytes cannot be empty")
)

// DeserializeResourceSpec deserializes a ResourceSpec from JSON bytes (as stored in DB).
func DeserializeResourceSpec(specBytes []byte, resourceType genDb.ResourceType) (*resourcev1.ResourceSpec, error) {
	if len(specBytes) == 0 {
		return nil, errEmptySpecBytes
	}

	switch resourceType {
	case genDb.ResourceTypeService:
		var serviceSpec resourcev1.ServiceSpec
		if err := protojson.Unmarshal(specBytes, &serviceSpec); err != nil {
			return nil, fmt.Errorf("failed to unmarshal service deployment spec: %w", err)
		}
		return &resourcev1.ResourceSpec{
			Spec: &resourcev1.ResourceSpec_Service{Service: &serviceSpec},
		}, nil
	default:
		return nil, fmt.Errorf("unknown resource type for deployment: %s", resourceType)
	}
}

// DeserializeDeploymentSpec deserializes a DeploymentSpec from JSON bytes based on the resource type.
// The specBytes should contain only the inner deployment spec (ServiceDeploymentSpec, etc.), not the wrapper.
func DeserializeDeploymentSpec(specBytes []byte, resourceType string) (*deploymentv1.DeploymentSpec, error) {
	if len(specBytes) == 0 {
		return nil, errEmptySpecBytes
	}

	switch resourceType {
	case "service":
		var serviceSpec deploymentv1.ServiceDeploymentSpec
		if err := protojson.Unmarshal(specBytes, &serviceSpec); err != nil {
			return nil, fmt.Errorf("failed to unmarshal service deployment spec: %w", err)
		}
		return &deploymentv1.DeploymentSpec{
			Spec: &deploymentv1.DeploymentSpec_Service{Service: &serviceSpec},
		}, nil
	default:
		return nil, fmt.Errorf("unknown resource type for deployment: %s", resourceType)
	}
}

// DeserializeResourceSpecByType deserializes a ResourceSpec from JSON bytes based on the resource type.
// The specBytes should contain only the inner spec (ServiceSpec, DatabaseSpec, etc.), not the wrapper.
func DeserializeResourceSpecByType(specBytes []byte, resourceType string) (*resourcev1.ResourceSpec, error) {
	if len(specBytes) == 0 {
		return nil, errEmptySpecBytes
	}

	switch resourceType {
	case "service":
		var serviceSpec resourcev1.ServiceSpec
		if err := protojson.Unmarshal(specBytes, &serviceSpec); err != nil {
			return nil, fmt.Errorf("failed to unmarshal service spec: %w", err)
		}
		return &resourcev1.ResourceSpec{
			Spec: &resourcev1.ResourceSpec_Service{Service: &serviceSpec},
		}, nil
	case "database":
		var databaseSpec resourcev1.DatabaseSpec
		if err := protojson.Unmarshal(specBytes, &databaseSpec); err != nil {
			return nil, fmt.Errorf("failed to unmarshal database spec: %w", err)
		}
		return &resourcev1.ResourceSpec{
			Spec: &resourcev1.ResourceSpec_Database{Database: &databaseSpec},
		}, nil
	case "cache":
		var cacheSpec resourcev1.CacheSpec
		if err := protojson.Unmarshal(specBytes, &cacheSpec); err != nil {
			return nil, fmt.Errorf("failed to unmarshal cache spec: %w", err)
		}
		return &resourcev1.ResourceSpec{
			Spec: &resourcev1.ResourceSpec_Cache{Cache: &cacheSpec},
		}, nil
	case "queue":
		var queueSpec resourcev1.QueueSpec
		if err := protojson.Unmarshal(specBytes, &queueSpec); err != nil {
			return nil, fmt.Errorf("failed to unmarshal queue spec: %w", err)
		}
		return &resourcev1.ResourceSpec{
			Spec: &resourcev1.ResourceSpec_Queue{Queue: &queueSpec},
		}, nil
	case "blob":
		var blobSpec resourcev1.BlobSpec
		if err := protojson.Unmarshal(specBytes, &blobSpec); err != nil {
			return nil, fmt.Errorf("failed to unmarshal blob spec: %w", err)
		}
		return &resourcev1.ResourceSpec{
			Spec: &resourcev1.ResourceSpec_Blob{Blob: &blobSpec},
		}, nil
	default:
		return nil, fmt.Errorf("unknown resource type: %s", resourceType)
	}
}

// MergeDeploymentSpec merges a request DeploymentSpec with the resource's region target and the
// API's configured defaults. The request takes precedence, then the region target, then the defaults.
// This is the API's single source of truth for deployment defaults.
func MergeDeploymentSpec(
	resourceSpec *resourcev1.ResourceSpec,
	requestSpec *deploymentv1.DeploymentSpec,
	region string,
	defaults servicedefaults.Defaults,
) (*deploymentv1.DeploymentSpec, error) {
	if resourceSpec == nil {
		return nil, errNilResourceSpec
	}
	if requestSpec == nil {
		return nil, errNilRequestSpec
	}
	if region == "" {
		return nil, errRegionRequired
	}

	// extract ServiceSpec from resourceSpec oneof
	resourceServiceSpec := resourceSpec.GetService()
	if resourceServiceSpec == nil {
		return nil, errResourceSpecNotService
	}

	// extract ServiceDeploymentSpec from requestSpec oneof
	requestServiceSpec := requestSpec.GetService()
	if requestServiceSpec == nil {
		return nil, errDeploymentSpecNotService
	}

	// find requested region in resource spec
	regionTarget, ok := resourceServiceSpec.GetRegions()[region]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrRegionNotFound, region)
	}
	if !regionTarget.GetEnabled() {
		return nil, fmt.Errorf("%w: %s", ErrRegionDisabled, region)
	}

	// merge build (from request, always required)
	mergedServiceSpec := &deploymentv1.ServiceDeploymentSpec{
		Build: requestServiceSpec.GetBuild(),
		Port:  requestServiceSpec.GetPort(),
		Env:   requestServiceSpec.GetEnv(),
	}

	cpu := firstSet(requestServiceSpec.GetCpu(), regionTarget.GetCpu(), defaults.CPU)
	memory := firstSet(requestServiceSpec.GetMemory(), regionTarget.GetMemory(), defaults.Memory)
	minReplicas := firstSet(requestServiceSpec.GetMinReplicas(), regionTarget.GetMinReplicas(), defaults.MinReplicas)
	maxReplicas := firstSet(requestServiceSpec.GetMaxReplicas(), regionTarget.GetMaxReplicas())
	if maxReplicas == 0 {
		maxReplicas = max(defaults.MaxReplicas, minReplicas)
	}
	mergedServiceSpec.Cpu = &cpu
	mergedServiceSpec.Memory = &memory
	mergedServiceSpec.MinReplicas = &minReplicas
	mergedServiceSpec.MaxReplicas = &maxReplicas

	// merge Scalers (request > resource default)
	if requestServiceSpec.GetScalers() != nil {
		mergedServiceSpec.Scalers = requestServiceSpec.GetScalers()
	} else if regionTarget.GetScalers() != nil {
		mergedServiceSpec.Scalers = regionTarget.GetScalers()
	}

	// merge HealthCheck (request > resource default)
	if requestServiceSpec.GetHealthCheck() != nil {
		mergedServiceSpec.HealthCheck = requestServiceSpec.GetHealthCheck()
	} else if resourceServiceSpec.GetHealthCheck() != nil {
		mergedServiceSpec.HealthCheck = resourceServiceSpec.GetHealthCheck()
	}

	// wrap merged ServiceDeploymentSpec in DeploymentSpec oneof
	mergedSpec := &deploymentv1.DeploymentSpec{
		Spec: &deploymentv1.DeploymentSpec_Service{
			Service: mergedServiceSpec,
		},
	}

	return mergedSpec, nil
}

func firstSet[T comparable](values ...T) T {
	var zero T
	for _, value := range values {
		if value != zero {
			return value
		}
	}
	return zero
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

func ProtoToRoutingSpec(
	routing *resourcev1.RoutingConfig,
	hostname string,
	defaults servicedefaults.Defaults,
) *locoControllerV1.RoutingSpec {
	if hostname == "" {
		return nil
	}

	return &locoControllerV1.RoutingSpec{
		HostName:    hostname,
		PathPrefix:  firstSet(routing.GetPathPrefix(), defaults.PathPrefix),
		IdleTimeout: firstSet(routing.GetIdleTimeout(), defaults.IdleTimeout),
	}
}

func ProtoToResourcesSpec(service *deploymentv1.ServiceDeploymentSpec) *locoControllerV1.ResourcesSpec {
	resources := &locoControllerV1.ResourcesSpec{
		CPU:    service.GetCpu(),
		Memory: service.GetMemory(),
		Replicas: locoControllerV1.ReplicasSpec{
			Min: service.GetMinReplicas(),
			Max: service.GetMaxReplicas(),
		},
	}
	if scalers := service.GetScalers(); scalers != nil {
		resources.Scalers = locoControllerV1.ScalersSpec{
			Enabled:      scalers.GetEnabled(),
			CPUTarget:    scalers.GetCpuTarget(),
			MemoryTarget: scalers.GetMemoryTarget(),
		}
	}
	return resources
}
