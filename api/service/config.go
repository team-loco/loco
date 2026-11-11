package service

import (
	"context"
	"time"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/logretention"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// ConfigServer implements the ConfigService, returning default values for use by the CLI and UI.
type ConfigServer struct {
	minCLIVersion       string
	apiTokenMaxLifetime time.Duration
	schemaURL           string
	defaults            servicedefaults.Defaults
	auth                *configv1.AuthConfig
	orgLinks            []*configv1.OrgLink
}

func NewConfigServer(
	minCLIVersion, schemaURL string,
	defaults servicedefaults.Defaults,
	issuers []auth.IssuerConfig,
	orgLinks []OrgLink,
	apiTokenMaxLifetime time.Duration,
) *ConfigServer {
	var authConfig *configv1.AuthConfig
	if ic, ok := auth.WebIssuer(issuers); ok {
		authConfig = &configv1.AuthConfig{Issuer: ic.Issuer, ClientId: ic.Web.ClientID, Scopes: ic.Web.Scopes}
	}
	return &ConfigServer{
		minCLIVersion:       minCLIVersion,
		schemaURL:           schemaURL,
		defaults:            defaults,
		orgLinks:            orgLinksProto(orgLinks),
		auth:                authConfig,
		apiTokenMaxLifetime: apiTokenMaxLifetime,
	}
}

func (s *ConfigServer) GetConfig(
	_ context.Context,
	_ *configv1.GetConfigRequest,
) (*configv1.GetConfigResponse, error) {
	health := configplan.Health(s.defaults.Health())
	return &configv1.GetConfigResponse{
		MinCliVersion:              s.minCLIVersion,
		ApiTokenMaxLifetimeSeconds: int64(s.apiTokenMaxLifetime / time.Second),
		SchemaUrl:                  s.schemaURL,
		Auth:                       s.auth,
		OrgLinks:                   s.orgLinks,
		SizeLimits: &configv1.SizeLimits{
			MinCpuMillis:   locoControllerV1.MinCPUMillis,
			MaxCpuMillis:   locoControllerV1.MaxCPUMillis,
			MinMemoryBytes: locoControllerV1.MinMemoryBytes,
			MaxMemoryBytes: locoControllerV1.MaxMemoryBytes,
			MinReplicas:    locoControllerV1.MinReplicas,
			MaxReplicas:    locoControllerV1.MaxReplicas,
		},
		ServiceDefaults: &configv1.DefaultServiceConfig{
			Routing: &resourcev1.RoutingConfig{
				Port:        s.defaults.Port,
				PathPrefix:  s.defaults.PathPrefix,
				IdleTimeout: s.defaults.IdleTimeout,
			},
			HealthCheck:             healthCheckFor(&health),
			Cpu:                     s.defaults.CPU,
			Memory:                  s.defaults.Memory,
			MinReplicas:             s.defaults.MinReplicas,
			MaxReplicas:             s.defaults.MaxReplicas,
			CpuTarget:               s.defaults.CPUTarget,
			DeployAttempts:          s.defaults.DeployAttempts,
			DeployRetryDelaySeconds: s.defaults.DeployRetryDelay,
			Observability: &resourcev1.ObservabilityConfig{
				Logging: &resourcev1.LoggingConfig{
					Enabled:         true,
					RetentionPeriod: logretention.Format(s.defaults.LogRetention),
					Structured:      false,
				},
				Metrics: &resourcev1.MetricsConfig{
					Enabled: false,
					Path:    s.defaults.MetricsPath,
					Port:    s.defaults.MetricsPort,
				},
			},
		},
	}, nil
}
