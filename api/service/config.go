package service

import (
	"context"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

// ConfigServer implements the ConfigService, returning default values for use by the CLI and UI.
type ConfigServer struct {
	minCLIVersion string
	schemaURL     string
	defaults      servicedefaults.Defaults
	auth          *configv1.AuthConfig
	orgLinks      []*configv1.OrgLink
}

func NewConfigServer(
	minCLIVersion, schemaURL string,
	defaults servicedefaults.Defaults,
	issuers []auth.IssuerConfig,
	orgLinks []OrgLink,
) *ConfigServer {
	s := &ConfigServer{
		minCLIVersion: minCLIVersion,
		schemaURL:     schemaURL,
		defaults:      defaults,
		orgLinks:      orgLinksProto(orgLinks),
	}
	if ic, ok := auth.WebIssuer(issuers); ok {
		s.auth = &configv1.AuthConfig{
			Issuer:   ic.Issuer,
			ClientId: ic.Web.ClientID,
			Scopes:   ic.Web.Scopes,
		}
	}
	return s
}

func (s *ConfigServer) GetConfig(
	_ context.Context,
	_ *configv1.GetConfigRequest,
) (*configv1.GetConfigResponse, error) {
	return &configv1.GetConfigResponse{
		MinCliVersion: s.minCLIVersion,
		SchemaUrl:     s.schemaURL,
		Auth:          s.auth,
		OrgLinks:      s.orgLinks,
		ServiceDefaults: &configv1.DefaultServiceConfig{
			Routing: &resourcev1.RoutingConfig{
				Port:        s.defaults.Port,
				PathPrefix:  s.defaults.PathPrefix,
				IdleTimeout: s.defaults.IdleTimeout,
			},
			HealthCheck: &deploymentv1.HealthCheckConfig{
				Path:                s.defaults.HealthPath,
				IntervalSeconds:     s.defaults.HealthInterval,
				TimeoutSeconds:      s.defaults.HealthTimeout,
				FailureThreshold:    s.defaults.HealthFailThreshold,
				InitialDelaySeconds: s.defaults.HealthStartupGracePeriod,
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
			},
		},
	}, nil
}
