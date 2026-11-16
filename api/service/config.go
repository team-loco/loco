package service

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/internal/observabilityconfig"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// ConfigServer implements the ConfigService, returning default values for use by the CLI and UI.
type ConfigServer struct {
	minCLIVersion             string
	apiTokenMaxLifetime       time.Duration
	observabilityMaxTimeRange time.Duration
	schemaURL                 string
	defaults                  servicedefaults.Defaults
	auth                      *configv1.AuthConfig
	orgLinks                  []*configv1.OrgLink
	counts                    *configv1.CountLimits
	historyRetention          *configv1.HistoryRetention
}

func NewConfigServer(
	minCLIVersion, schemaURL string,
	defaults servicedefaults.Defaults,
	issuers []auth.IssuerConfig,
	orgLinks []OrgLink,
	apiTokenMaxLifetime, observabilityMaxTimeRange time.Duration,
	counts *configv1.CountLimits,
	historyRetention *configv1.HistoryRetention,
) *ConfigServer {
	var authConfig *configv1.AuthConfig
	if ic, ok := auth.WebIssuer(issuers); ok {
		authConfig = &configv1.AuthConfig{Issuer: ic.Issuer, ClientId: ic.Web.ClientID, Scopes: ic.Web.Scopes}
	}
	return &ConfigServer{
		minCLIVersion:             minCLIVersion,
		schemaURL:                 schemaURL,
		defaults:                  defaults,
		orgLinks:                  orgLinksProto(orgLinks),
		auth:                      authConfig,
		apiTokenMaxLifetime:       apiTokenMaxLifetime,
		observabilityMaxTimeRange: observabilityMaxTimeRange,
		counts:                    counts,
		historyRetention:          historyRetention,
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
		CountLimits:                s.counts,
		HistoryRetention:           s.historyRetention,
		ObservabilityMaxTimeRange:  durationpb.New(s.observabilityMaxTimeRange),
		SizeLimits: &configv1.SizeLimits{
			MinCpuMillis:   locoControllerV1.MinCPUMillis,
			MaxCpuMillis:   locoControllerV1.MaxCPUMillis,
			MinMemoryBytes: locoControllerV1.MinMemoryBytes,
			MaxMemoryBytes: locoControllerV1.MaxMemoryBytes,
			MinReplicas:    locoControllerV1.MinReplicas,
			MaxReplicas:    locoControllerV1.MaxReplicas,
		},
		ServiceDefaults: &configv1.DefaultServiceConfig{
			Observability:           observabilityconfig.Defaults().Proto(),
			HealthCheck:             healthCheckFor(&health),
			Cpu:                     s.defaults.CPU,
			Memory:                  s.defaults.Memory,
			MinReplicas:             s.defaults.MinReplicas,
			MaxReplicas:             s.defaults.MaxReplicas,
			CpuTarget:               s.defaults.CPUTarget,
			DeployAttempts:          s.defaults.DeployAttempts,
			DeployRetryDelaySeconds: s.defaults.DeployRetryDelay,
		},
	}, nil
}
