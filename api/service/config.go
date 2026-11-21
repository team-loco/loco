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
	apiVersion                string
	minCLIVersion             string
	apiTokenMaxLifetime       time.Duration
	observabilityMaxTimeRange time.Duration
	schemaURL                 string
	defaults                  servicedefaults.Defaults
	webAuthEnabled            bool
	orgLinks                  []*configv1.OrgLink
	counts                    *configv1.CountLimits
	historyRetention          *configv1.HistoryRetention
	defaultRegion             string
	regions                   liveRegionChecker
	warnDefaultRegion         sync.Once
}

type liveRegionChecker interface {
	IsLiveRegion(ctx context.Context, code string) (bool, error)
}

func NewConfigServer(
	apiVersion, minCLIVersion, schemaURL string,
	defaults servicedefaults.Defaults,
	issuers []auth.IssuerConfig,
	orgLinks []OrgLink,
	apiTokenMaxLifetime, observabilityMaxTimeRange time.Duration,
	counts *configv1.CountLimits,
	historyRetention *configv1.HistoryRetention,
	defaultRegion string,
	regions liveRegionChecker,
) *ConfigServer {
	_, webAuthEnabled := auth.WebIssuer(issuers)
	return &ConfigServer{
		apiVersion:                apiVersion,
		minCLIVersion:             minCLIVersion,
		schemaURL:                 schemaURL,
		defaults:                  defaults,
		orgLinks:                  orgLinksProto(orgLinks),
		webAuthEnabled:            webAuthEnabled,
		apiTokenMaxLifetime:       apiTokenMaxLifetime,
		observabilityMaxTimeRange: observabilityMaxTimeRange,
		counts:                    counts,
		historyRetention:          historyRetention,
		defaultRegion:             defaultRegion,
		regions:                   regions,
	}
}

// liveDefaultRegion returns LOCO_DEFAULT_REGION when it names a live region, and "" otherwise.
func (s *ConfigServer) liveDefaultRegion(ctx context.Context) (string, error) {
	if s.defaultRegion == "" {
		return "", nil
	}
	live, err := s.regions.IsLiveRegion(ctx, s.defaultRegion)
	if err != nil {
		return "", fmt.Errorf("check default region: %w", err)
	}
	if !live {
		s.warnDefaultRegion.Do(func() {
			slog.WarnContext(ctx, "LOCO_DEFAULT_REGION is not a live region; ignoring it", "region", s.defaultRegion)
		})
		return "", nil
	}
	return s.defaultRegion, nil
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
	health := configplan.Health(s.defaults.Health())
	return &configv1.GetConfigResponse{
		MinCliVersion:              s.minCLIVersion,
		ApiVersion:                 s.apiVersion,
		ApiTokenMaxLifetimeSeconds: int64(s.apiTokenMaxLifetime / time.Second),
		SchemaUrl:                  s.schemaURL,
		WebAuthEnabled:             s.webAuthEnabled,
		DefaultRegion:              defaultRegion,
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
			Observability: observabilityconfig.Defaults().Proto(),
			HealthCheck:   healthCheckFor(&health),
			Cpu:           s.defaults.CPU,
			Memory:        s.defaults.Memory,
			MinReplicas:   s.defaults.MinReplicas,
			MaxReplicas:   s.defaults.MaxReplicas,
			CpuTarget:     s.defaults.CPUTarget,
			FailurePolicy: s.defaults.FailurePolicy(nil, nil, nil).Proto(),
		},
	}, nil
}
