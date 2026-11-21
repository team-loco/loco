package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/internal/observabilityconfig"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// configCacheControl lets shared caches keep the response briefly; it holds only process-wide values.
const configCacheControl = "public, max-age=60"

// ConfigServer implements the ConfigService, returning default values for use by the CLI and UI.
type ConfigServer struct {
	settings          ConfigSettings
	webAuthEnabled    bool
	orgLinks          []*configv1.OrgLink
	regions           liveRegionChecker
	warnDefaultRegion sync.Once
}

// ConfigSettings holds the install-wide values GetConfig reports.
type ConfigSettings struct {
	APIVersion                string
	MinCLIVersion             string
	SchemaURL                 string
	ServiceDefaults           servicedefaults.Defaults
	AuthIssuers               []auth.IssuerConfig
	OrgLinks                  []OrgLink
	APITokenMaxLifetime       time.Duration
	ObservabilityMaxTimeRange time.Duration
	CountLimits               *configv1.CountLimits
	HistoryRetention          *configv1.HistoryRetention
	DefaultRegion             string
}

type liveRegionChecker interface {
	IsLiveRegion(ctx context.Context, code string) (bool, error)
}

func NewConfigServer(settings ConfigSettings, regions liveRegionChecker) *ConfigServer {
	_, webAuthEnabled := auth.WebIssuer(settings.AuthIssuers)
	return &ConfigServer{
		settings:       settings,
		webAuthEnabled: webAuthEnabled,
		orgLinks:       orgLinksProto(settings.OrgLinks),
		regions:        regions,
	}
}

// liveDefaultRegion returns LOCO_DEFAULT_REGION when it names a live region, and "" otherwise.
func (s *ConfigServer) liveDefaultRegion(ctx context.Context) (string, error) {
	if s.settings.DefaultRegion == "" {
		return "", nil
	}
	live, err := s.regions.IsLiveRegion(ctx, s.settings.DefaultRegion)
	if err != nil {
		return "", fmt.Errorf("check default region: %w", err)
	}
	if !live {
		s.warnDefaultRegion.Do(func() {
			slog.WarnContext(ctx, "LOCO_DEFAULT_REGION is not a live region; ignoring it",
				"region", s.settings.DefaultRegion)
		})
		return "", nil
	}
	return s.settings.DefaultRegion, nil
}

func (s *ConfigServer) GetConfig(
	ctx context.Context,
	_ *configv1.GetConfigRequest,
) (*configv1.GetConfigResponse, error) {
	if info, ok := connect.CallInfoForServerContext(ctx); ok {
		info.ResponseHeader().Set("Cache-Control", configCacheControl)
	}
	defaultRegion, err := s.liveDefaultRegion(ctx)
	if err != nil {
		return nil, err
	}
	health := configplan.Health(s.settings.ServiceDefaults.Health())
	return &configv1.GetConfigResponse{
		MinCliVersion:              s.settings.MinCLIVersion,
		ApiVersion:                 s.settings.APIVersion,
		ApiTokenMaxLifetimeSeconds: int64(s.settings.APITokenMaxLifetime / time.Second),
		SchemaUrl:                  s.settings.SchemaURL,
		WebAuthEnabled:             s.webAuthEnabled,
		DefaultRegion:              defaultRegion,
		OrgLinks:                   s.orgLinks,
		CountLimits:                s.settings.CountLimits,
		HistoryRetention:           s.settings.HistoryRetention,
		ObservabilityMaxTimeRange:  durationpb.New(s.settings.ObservabilityMaxTimeRange),
		SizeLimits: &configv1.SizeLimits{
			MinCpuMillis:   locoControllerV1.MinCPUMillis,
			MaxCpuMillis:   locoControllerV1.MaxCPUMillis,
			MinMemoryBytes: locoControllerV1.MinMemoryBytes,
			MaxMemoryBytes: locoControllerV1.MaxMemoryBytes,
			MinReplicas:    locoControllerV1.MinReplicas,
			MaxReplicas:    locoControllerV1.MaxReplicas,
		},
		ServiceDefaults: &configv1.DefaultServiceConfig{
			Observability: observabilityconfig.Defaults().Proto(),
			HealthCheck:   healthCheckFor(&health),
			Cpu:           s.settings.ServiceDefaults.CPU,
			Memory:        s.settings.ServiceDefaults.Memory,
			MinReplicas:   s.settings.ServiceDefaults.MinReplicas,
			MaxReplicas:   s.settings.ServiceDefaults.MaxReplicas,
			CpuTarget:     s.settings.ServiceDefaults.CPUTarget,
			FailurePolicy: s.settings.ServiceDefaults.FailurePolicy(nil, nil, nil).Proto(),
		},
	}, nil
}
