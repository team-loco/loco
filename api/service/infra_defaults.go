package service

import (
	"slices"
	"strings"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	infrav1 "github.com/team-loco/loco/gen/go/loco/infra/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func applyInfraDefaults(manifest *infrav1.StackManifest) {
	defaults := defaultServiceConfig("")
	manifest.Version = infraProtocolVersion
	slices.SortFunc(
		manifest.Services,
		func(a, b *infrav1.ServiceManifest) int { return strings.Compare(a.GetKey(), b.GetKey()) },
	)
	for _, service := range manifest.GetServices() {
		if service.GetName() == "" {
			service.Name = service.GetKey()
		}
		service.Hostname = strings.ToLower(service.GetHostname())
		spec := service.GetSpec().GetService()
		if spec.GetRouting() == nil {
			spec.Routing = &resourcev1.RoutingConfig{}
		}
		routing := spec.GetRouting()
		if routing.GetPort() == 0 {
			routing.Port = defaults.GetRouting().GetPort()
		}
		if routing.GetPathPrefix() == "" {
			routing.PathPrefix = defaults.GetRouting().GetPathPrefix()
		}
		if !infraFieldPresent(routing, "idle_timeout") {
			routing.IdleTimeout = proto.Int32(defaults.GetRouting().GetIdleTimeout())
		}
		if spec.GetHealthCheck() == nil {
			spec.HealthCheck = &deploymentv1.HealthCheckConfig{}
		}
		health := spec.GetHealthCheck()
		if health.GetPath() == "" {
			health.Path = defaults.GetHealthCheck().GetPath()
		}
		if health.GetIntervalSeconds() == 0 {
			health.IntervalSeconds = defaults.GetHealthCheck().GetIntervalSeconds()
		}
		if health.GetTimeoutSeconds() == 0 {
			health.TimeoutSeconds = defaults.GetHealthCheck().GetTimeoutSeconds()
		}
		if health.GetFailureThreshold() == 0 {
			health.FailureThreshold = defaults.GetHealthCheck().GetFailureThreshold()
		}
		for _, region := range spec.GetRegions() {
			if region.GetCpu() == "" {
				region.Cpu = defaults.GetCpu()
			}
			if region.GetMemory() == "" {
				region.Memory = defaults.GetMemory()
			}
			if region.GetMinReplicas() == 0 {
				region.MinReplicas = defaults.GetMinReplicas()
			}
			if region.GetMaxReplicas() == 0 {
				region.MaxReplicas = defaults.GetMaxReplicas()
			}
		}
		applyInfraObservabilityDefaults(spec, defaults.GetObservability())
	}
}

func applyInfraObservabilityDefaults(spec *resourcev1.ServiceSpec, defaults *resourcev1.ObservabilityConfig) {
	if spec.GetObservability() == nil {
		spec.Observability = &resourcev1.ObservabilityConfig{}
	}
	obs := spec.GetObservability()
	if obs.GetLogging() == nil {
		obs.Logging = &resourcev1.LoggingConfig{}
	}
	logging := obs.GetLogging()
	if !infraFieldPresent(logging, "enabled") {
		logging.Enabled = proto.Bool(defaults.GetLogging().GetEnabled())
	}
	if logging.GetRetentionPeriod() == "" {
		logging.RetentionPeriod = defaults.GetLogging().GetRetentionPeriod()
	}
	if obs.GetMetrics() == nil {
		obs.Metrics = &resourcev1.MetricsConfig{}
	}
	metrics := obs.GetMetrics()
	if metrics.GetPath() == "" {
		metrics.Path = defaults.GetMetrics().GetPath()
	}
	if metrics.GetPort() == 0 {
		metrics.Port = defaults.GetMetrics().GetPort()
	}
	if obs.GetTracing() == nil {
		obs.Tracing = &resourcev1.TracingConfig{}
	}
	tracing := obs.GetTracing()
	if !infraFieldPresent(tracing, "sample_rate") {
		tracing.SampleRate = proto.Float64(defaults.GetTracing().GetSampleRate())
	}
}

func infraFieldPresent(message proto.Message, name protoreflect.Name) bool {
	value := message.ProtoReflect()
	return value.Has(value.Descriptor().Fields().ByName(name))
}
