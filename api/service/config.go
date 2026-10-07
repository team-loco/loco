package service

import (
	"context"

	"connectrpc.com/connect"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"google.golang.org/protobuf/proto"
)

// ConfigServer implements the ConfigService, returning default values for use by the CLI and UI.
type ConfigServer struct {
	platformDomain string
	minCLIVersion  string
}

func NewConfigServer(platformDomain, minCLIVersion string) *ConfigServer {
	return &ConfigServer{platformDomain: platformDomain, minCLIVersion: minCLIVersion}
}

func (s *ConfigServer) GetConfig(
	_ context.Context,
	_ *connect.Request[configv1.GetConfigRequest],
) (*connect.Response[configv1.GetConfigResponse], error) {
	return connect.NewResponse(&configv1.GetConfigResponse{
		MinCliVersion:   s.minCLIVersion,
		ServiceDefaults: defaultServiceConfig(s.platformDomain),
	}), nil
}

func defaultServiceConfig(platformDomain string) *configv1.DefaultServiceConfig {
	return &configv1.DefaultServiceConfig{
		BuildType:      "docker",
		DockerfilePath: "Dockerfile",
		Routing: &resourcev1.RoutingConfig{
			Port:        8000,
			PathPrefix:  "/",
			IdleTimeout: proto.Int32(60),
		},
		HealthCheck: &deploymentv1.HealthCheckConfig{
			Path:                "/health",
			IntervalSeconds:     30,
			TimeoutSeconds:      5,
			FailureThreshold:    3,
			InitialDelaySeconds: 0,
		},
		Cpu:         "100m",
		Memory:      "256Mi",
		MinReplicas: 1,
		MaxReplicas: 1,
		Observability: &resourcev1.ObservabilityConfig{
			Logging: &resourcev1.LoggingConfig{
				Enabled:         proto.Bool(true),
				RetentionPeriod: "7d",
				Structured:      false,
			},
			Metrics: &resourcev1.MetricsConfig{
				Enabled: false,
				Path:    "/metrics",
				Port:    9090,
			},
			Tracing: &resourcev1.TracingConfig{
				Enabled:    false,
				SampleRate: proto.Float64(0.1),
			},
		},
		PlatformDomain: platformDomain,
	}
}
