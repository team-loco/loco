package loco

import (
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	infrav1 "github.com/team-loco/loco/gen/go/loco/infra/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const ProtocolVersion = 1

type Context = infrav1.AuthoringContext
type Manifest = infrav1.EvaluationManifest
type Stack = infrav1.StackManifest
type Service = infrav1.ServiceManifest
type DockerBuild = infrav1.DockerSource
type ServiceSpec = resourcev1.ServiceSpec
type Routing = resourcev1.RoutingConfig
type Region = resourcev1.RegionTarget
type Autoscaling = deploymentv1.Scalers
type Health = deploymentv1.HealthCheckConfig
type Observability = resourcev1.ObservabilityConfig
type Logging = resourcev1.LoggingConfig
type Metrics = resourcev1.MetricsConfig
type Tracing = resourcev1.TracingConfig
type Variable = infrav1.Variable

func Docker(context, dockerfile string) *infrav1.ServiceManifest_Docker {
	return &infrav1.ServiceManifest_Docker{Docker: &DockerBuild{Context: context, Dockerfile: dockerfile}}
}

func Image(reference string) *infrav1.ServiceManifest_Image {
	return &infrav1.ServiceManifest_Image{Image: reference}
}

func Config(spec *ServiceSpec) *resourcev1.ResourceSpec {
	return &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: spec}}
}

func Literal(value string) *Variable {
	return &Variable{Expression: &infrav1.Variable_Literal{Literal: value}}
}

func SecretRef(name string) *Variable {
	return &Variable{Expression: &infrav1.Variable_Secret{Secret: name}}
}

func Preserve() *Variable {
	return &Variable{Expression: &infrav1.Variable_Preserve{Preserve: true}}
}

func Value[T any](value T) *T {
	return &value
}
