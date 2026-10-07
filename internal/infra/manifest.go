package infra

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	infrav1 "github.com/team-loco/loco/gen/go/loco/infra/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	loco "github.com/team-loco/loco/sdk/go"
	"google.golang.org/protobuf/proto"
)

func ProtoManifest(manifest *loco.Manifest) (*infrav1.StackManifest, error) {
	if err := loco.Normalize(manifest); err != nil {
		return nil, err
	}
	result := &infrav1.StackManifest{Version: uint32(manifest.Version), Name: manifest.Stack.Name}
	for _, service := range manifest.Stack.Services {
		result.Services = append(result.Services, protoService(service))
	}
	return result, nil
}

func protoService(service loco.Service) *infrav1.ServiceManifest {
	spec := &resourcev1.ServiceSpec{
		Routing: &resourcev1.RoutingConfig{
			Port: service.Routing.Port, PathPrefix: service.Routing.PathPrefix,
			IdleTimeout: *service.Routing.IdleTimeout,
		},
		HealthCheck: &deploymentv1.HealthCheckConfig{
			Path: service.Health.Path, IntervalSeconds: service.Health.Interval,
			TimeoutSeconds: service.Health.Timeout, InitialDelaySeconds: service.Health.StartupGracePeriod,
			FailureThreshold: service.Health.FailThreshold,
		},
		Regions: make(map[string]*resourcev1.RegionTarget, len(service.Regions)),
		Observability: &resourcev1.ObservabilityConfig{
			Logging: &resourcev1.LoggingConfig{
				Enabled:         *service.Observability.Logging.Enabled,
				RetentionPeriod: service.Observability.Logging.RetentionPeriod,
				Structured:      service.Observability.Logging.Structured,
			},
			Metrics: &resourcev1.MetricsConfig{
				Enabled: service.Observability.Metrics.Enabled, Path: service.Observability.Metrics.Path,
				Port: service.Observability.Metrics.Port,
			},
			Tracing: &resourcev1.TracingConfig{
				Enabled: service.Observability.Tracing.Enabled, SampleRate: *service.Observability.Tracing.SampleRate,
				Tags: service.Observability.Tracing.Tags,
			},
		},
	}
	for name, region := range service.Regions {
		target := &resourcev1.RegionTarget{
			Enabled: true, Primary: name == service.PrimaryRegion, Cpu: region.CPU, Memory: region.Memory,
			MinReplicas: region.ReplicasMin, MaxReplicas: region.ReplicasMax,
		}
		if region.Autoscaling != nil {
			target.Scalers = &deploymentv1.Scalers{
				Enabled: true, CpuTarget: region.Autoscaling.CPUTarget, MemoryTarget: region.Autoscaling.MemoryTarget,
			}
		}
		spec.Regions[name] = target
	}
	result := &infrav1.ServiceManifest{
		Key: service.Key, Name: service.Name, Description: service.Description,
		Spec:      &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: spec}},
		Variables: make(map[string]*infrav1.Variable, len(service.Env)),
	}
	if service.Domain != nil {
		result.Hostname = service.Domain.Hostname
	}
	if service.Build != nil {
		result.Source = &infrav1.ServiceManifest_Docker{Docker: &infrav1.DockerSource{
			Context: service.Build.Context, Dockerfile: service.Build.Dockerfile,
		}}
	} else {
		result.Source = &infrav1.ServiceManifest_Image{Image: service.Image}
	}
	for name, variable := range service.Env {
		value := &infrav1.Variable{}
		switch variable.Kind {
		case loco.VariableLiteral:
			value.Expression = &infrav1.Variable_Literal{Literal: variable.Value}
		case loco.VariableSecret:
			value.Expression = &infrav1.Variable_Secret{Secret: variable.Name}
		case loco.VariablePreserve:
			value.Expression = &infrav1.Variable_Preserve{Preserve: true}
		}
		result.Variables[name] = value
	}
	return result
}

func AuthorManifest(manifest *infrav1.StackManifest) (*loco.Manifest, error) {
	if manifest == nil {
		return nil, fmt.Errorf("manifest is required")
	}
	result := &loco.Manifest{Version: int(manifest.GetVersion()), Stack: loco.Stack{Name: manifest.GetName()}}
	for _, service := range manifest.GetServices() {
		value, err := authorService(service)
		if err != nil {
			return nil, err
		}
		result.Stack.Services = append(result.Stack.Services, value)
	}
	if err := loco.Normalize(result); err != nil {
		return nil, err
	}
	return result, nil
}

func authorService(service *infrav1.ServiceManifest) (loco.Service, error) {
	spec := service.GetSpec().GetService()
	if spec == nil || spec.GetRouting() == nil || spec.GetHealthCheck() == nil {
		return loco.Service{}, fmt.Errorf("service %q requires service spec, routing and health", service.GetKey())
	}
	value := loco.Service{
		Key: service.GetKey(), Name: service.GetName(), Description: service.GetDescription(),
		Routing: loco.Routing{
			Port: spec.GetRouting().GetPort(), PathPrefix: spec.GetRouting().GetPathPrefix(),
			IdleTimeout: loco.Value(spec.GetRouting().GetIdleTimeout()),
		},
		Health: loco.Health{
			Path:     spec.GetHealthCheck().GetPath(),
			Interval: spec.GetHealthCheck().GetIntervalSeconds(),
			Timeout: spec.GetHealthCheck().
				GetTimeoutSeconds(),
			StartupGracePeriod: spec.GetHealthCheck().GetInitialDelaySeconds(),
			FailThreshold:      spec.GetHealthCheck().GetFailureThreshold(),
		},
		Regions: make(map[string]loco.Region, len(spec.GetRegions())),
		Env:     make(map[string]loco.Variable, len(service.GetVariables())),
	}
	if service.GetHostname() != "" {
		value.Domain = &loco.PlatformDomain{Hostname: service.GetHostname()}
	}
	switch source := service.GetSource().(type) {
	case *infrav1.ServiceManifest_Docker:
		value.Build = &loco.DockerBuild{Context: source.Docker.GetContext(), Dockerfile: source.Docker.GetDockerfile()}
	case *infrav1.ServiceManifest_Image:
		value.Image = source.Image
	default:
		return value, fmt.Errorf("service %q requires a source", service.GetKey())
	}
	for name, region := range spec.GetRegions() {
		if !region.GetEnabled() {
			return value, fmt.Errorf("region %q must be enabled or omitted", name)
		}
		if region.GetPrimary() {
			if value.PrimaryRegion != "" {
				return value, fmt.Errorf("service %q has multiple primary regions", service.GetKey())
			}
			value.PrimaryRegion = name
		}
		target := loco.Region{
			CPU:         region.GetCpu(),
			Memory:      region.GetMemory(),
			ReplicasMin: region.GetMinReplicas(),
			ReplicasMax: region.GetMaxReplicas(),
		}
		if region.GetScalers().GetEnabled() {
			target.Autoscaling = &loco.Autoscaling{
				CPUTarget: nil, MemoryTarget: nil,
			}
			if cpu := region.GetScalers().GetCpuTarget(); cpu != 0 {
				target.Autoscaling.CPUTarget = loco.Value(cpu)
			}
			if memory := region.GetScalers().GetMemoryTarget(); memory != 0 {
				target.Autoscaling.MemoryTarget = loco.Value(memory)
			}
		}
		value.Regions[name] = target
	}
	for name, variable := range service.GetVariables() {
		switch expression := variable.GetExpression().(type) {
		case *infrav1.Variable_Literal:
			value.Env[name] = loco.Literal(expression.Literal)
		case *infrav1.Variable_Secret:
			value.Env[name] = loco.SecretRef(expression.Secret)
		case *infrav1.Variable_Preserve:
			if !expression.Preserve {
				return value, fmt.Errorf("preserve expression for %q must be true", name)
			}
			value.Env[name] = loco.Preserve()
		default:
			return value, fmt.Errorf("variable %q requires an expression", name)
		}
	}
	obs := spec.GetObservability()
	value.Observability = loco.Observability{
		Logging: loco.Logging{
			Enabled: loco.Value(obs.GetLogging().GetEnabled()), RetentionPeriod: obs.GetLogging().GetRetentionPeriod(),
			Structured: obs.GetLogging().GetStructured(),
		},
		Metrics: loco.Metrics{
			Enabled: obs.GetMetrics().GetEnabled(), Path: obs.GetMetrics().GetPath(), Port: obs.GetMetrics().GetPort(),
		},
		Tracing: loco.Tracing{
			Enabled: obs.GetTracing().GetEnabled(), SampleRate: loco.Value(obs.GetTracing().GetSampleRate()),
			Tags: obs.GetTracing().GetTags(),
		},
	}
	return value, nil
}

func PlanDigest(plan *infrav1.Plan) (string, error) {
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(plan)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
