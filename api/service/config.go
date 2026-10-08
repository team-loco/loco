package service

import (
	"context"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

// ConfigServer implements the ConfigService, returning default values for use by the CLI and UI.
type ConfigServer struct {
	platformDomain string
	minCLIVersion  string
	defaults       servicedefaults.Defaults
}

func NewConfigServer(platformDomain, minCLIVersion string, defaults servicedefaults.Defaults) *ConfigServer {
	return &ConfigServer{platformDomain: platformDomain, minCLIVersion: minCLIVersion, defaults: defaults}
}

func (s *ConfigServer) GetConfig(
	_ context.Context,
	_ *connect.Request[configv1.GetConfigRequest],
) (*connect.Response[configv1.GetConfigResponse], error) {
	return connect.NewResponse(&configv1.GetConfigResponse{
		MinCliVersion: s.minCLIVersion,
		ServiceDefaults: &configv1.DefaultServiceConfig{
			Routing: &resourcev1.RoutingConfig{
				Port:        8000,
				PathPrefix:  s.defaults.PathPrefix,
				IdleTimeout: s.defaults.IdleTimeout,
			},
			HealthCheck: &deploymentv1.HealthCheckConfig{
				Path:                "/health",
				IntervalSeconds:     30,
				TimeoutSeconds:      5,
				FailureThreshold:    3,
				InitialDelaySeconds: 0,
			},
			Cpu:         s.defaults.CPU,
			Memory:      s.defaults.Memory,
			MinReplicas: s.defaults.MinReplicas,
			MaxReplicas: s.defaults.MaxReplicas,
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
