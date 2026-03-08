package service

import (
	"context"

	"connectrpc.com/connect"
	configv1 "github.com/team-loco/loco/proto/loco/config/v1"
	deploymentv1 "github.com/team-loco/loco/proto/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/proto/loco/resource/v1"
)

// ConfigServer implements the ConfigService, returning default values for use by the CLI and UI.
type ConfigServer struct {
	platformDomain string
}

func NewConfigServer(platformDomain string) *ConfigServer {
	return &ConfigServer{platformDomain: platformDomain}
}

func (s *ConfigServer) GetDefaultServiceConfig(
	ctx context.Context,
	req *connect.Request[configv1.GetDefaultServiceConfigRequest],
) (*connect.Response[configv1.GetDefaultServiceConfigResponse], error) {
	return connect.NewResponse(&configv1.GetDefaultServiceConfigResponse{
		Config: &configv1.DefaultServiceConfig{
			BuildType:      "docker",
			DockerfilePath: "Dockerfile",
			Routing: &resourcev1.RoutingConfig{
				Port:        8000,
				PathPrefix:  "/",
				IdleTimeout: 60,
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
					Enabled:         true,
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
					SampleRate: 0.1,
				},
			},
			PlatformDomain: s.platformDomain,
		},
	}), nil
}
