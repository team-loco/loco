package internal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/internal/ui"
	"github.com/team-loco/loco/shared/config"
	domainv1 "github.com/team-loco/loco/shared/proto/loco/domain/v1"
	resourcev1 "github.com/team-loco/loco/shared/proto/loco/resource/v1"
)

// ConfigGatherer handles gathering configuration interactively when loco.toml is not present.
type ConfigGatherer struct {
	deps *ServiceDeps
}

// NewConfigGatherer creates a new ConfigGatherer.
func NewConfigGatherer(deps *ServiceDeps) *ConfigGatherer {
	return &ConfigGatherer{deps: deps}
}

// GatherDeployConfig gathers all necessary configuration for deployment.
// It uses smart batching - groups related prompts together.
func (g *ConfigGatherer) GatherDeployConfig(ctx context.Context, name string) (*config.LoadedConfig, error) {
	// Get region
	region, err := g.selectRegion(ctx)
	if err != nil {
		return nil, fmt.Errorf("region selection failed: %w", err)
	}

	// Get domain info
	domainInfo, err := g.selectDomain(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("domain selection failed: %w", err)
	}

	// Build config with sensible defaults
	cfg := &config.LocoConfig{
		Metadata: config.Metadata{
			ConfigVersion: "1",
			Name:          name,
			Region:        region,
		},
		Build: config.Build{
			Type:           "dockerfile",
			DockerfilePath: "Dockerfile",
		},
		Routing: config.Routing{
			Port: 8080,
		},
		DomainConfig: domainInfo,
		RegionConfig: map[string]config.Resources{
			region: {
				CPU:         "0.25",
				Memory:      "256Mi",
				ReplicasMin: 1,
				ReplicasMax: 1,
			},
		},
		Health: config.Health{
			Path:               "/health",
			StartupGracePeriod: 30,
			Interval:           10,
			Timeout:            5,
			FailThreshold:      3,
		},
		Obs: config.Obs{
			Logging: config.Logging{
				Enabled:         true,
				RetentionPeriod: "7d",
				Structured:      true,
			},
			Metrics: config.Metrics{
				Enabled: false,
			},
			Tracing: config.Tracing{
				Enabled: false,
			},
		},
	}

	// Get current working directory as project path
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	return &config.LoadedConfig{
		Config:      cfg,
		ProjectPath: cwd,
	}, nil
}

// selectRegion prompts the user to select a region.
func (g *ConfigGatherer) selectRegion(ctx context.Context) (string, error) {
	req := connect.NewRequest(&resourcev1.ListRegionsRequest{})
	req.Header().Set("Authorization", g.deps.AuthHeader())

	resp, err := g.deps.ListRegions(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch regions: %w", err)
	}

	if len(resp.Msg.Regions) == 0 {
		return "", errors.New("no available regions found")
	}

	// If only one region, use it directly
	if len(resp.Msg.Regions) == 1 {
		region := resp.Msg.Regions[0].Region
		slog.Info("using only available region", "region", region)
		return region, nil
	}

	// Build options for selection
	options := make([]ui.SelectOption, len(resp.Msg.Regions))
	for i, r := range resp.Msg.Regions {
		label := r.Region
		if r.IsDefault {
			label += " (default)"
		}
		options[i] = ui.SelectOption{
			Label:       label,
			Description: fmt.Sprintf("Health: %s", r.HealthStatus),
			Value:       r.Region,
		}
	}

	selected, err := g.deps.SelectFromList("Select a region for your service", options)
	if err != nil {
		return "", err
	}

	region, ok := selected.(string)
	if !ok {
		return "", fmt.Errorf("invalid region selection: expected string, got %T", selected)
	}

	return region, nil
}

// DomainInfo contains the resolved domain configuration.
type DomainInfo struct {
	Type             string // "platform" or "custom"
	Hostname         string
	Subdomain        string
	PlatformDomainID int64
}

// selectDomain prompts the user to select a domain.
func (g *ConfigGatherer) selectDomain(ctx context.Context, serviceName string) (config.DomainConfig, error) {
	activeOnly := true
	req := connect.NewRequest(&domainv1.ListPlatformDomainsRequest{
		ActiveOnly: &activeOnly,
	})
	req.Header().Set("Authorization", g.deps.AuthHeader())

	resp, err := g.deps.ListPlatformDomains(ctx, req)
	if err != nil {
		return config.DomainConfig{}, fmt.Errorf("failed to fetch platform domains: %w", err)
	}

	if len(resp.Msg.PlatformDomains) == 0 {
		return config.DomainConfig{}, errors.New("no available platform domains found")
	}

	// Build options - use subdomain format
	options := make([]ui.SelectOption, len(resp.Msg.PlatformDomains))
	for i, domain := range resp.Msg.PlatformDomains {
		hostname := fmt.Sprintf("%s.%s", serviceName, domain.Domain)
		options[i] = ui.SelectOption{
			Label:       hostname,
			Description: fmt.Sprintf("Platform domain: %s", domain.Domain),
			Value:       domain,
		}
	}

	selected, err := g.deps.SelectFromList("Select a domain for your service", options)
	if err != nil {
		return config.DomainConfig{}, err
	}

	domain, ok := selected.(*domainv1.PlatformDomain)
	if !ok {
		return config.DomainConfig{}, fmt.Errorf("invalid domain selection: expected *PlatformDomain, got %T", selected)
	}

	hostname := fmt.Sprintf("%s.%s", serviceName, domain.Domain)

	return config.DomainConfig{
		Type:     "platform",
		Hostname: hostname,
	}, nil
}

// ResolveDomainInput creates the domain input for resource creation.
func (g *ConfigGatherer) ResolveDomainInput(ctx context.Context, cfg *config.LocoConfig) (*domainv1.DomainInput, error) {
	if cfg.DomainConfig.Type == "custom" {
		return &domainv1.DomainInput{
			DomainSource: domainv1.DomainType_DOMAIN_TYPE_USER_PROVIDED,
			Domain:       &cfg.DomainConfig.Hostname,
		}, nil
	}

	// Platform domain - need to resolve the base domain
	subdomain := config.ExtractSubdomainFromHostname(cfg.DomainConfig.Hostname)
	if subdomain == "" {
		return nil, errors.New("failed to extract subdomain from hostname")
	}

	activeOnly := true
	req := connect.NewRequest(&domainv1.ListPlatformDomainsRequest{
		ActiveOnly: &activeOnly,
	})
	req.Header().Set("Authorization", g.deps.AuthHeader())

	resp, err := g.deps.ListPlatformDomains(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch platform domains: %w", err)
	}

	// Find matching platform domain
	var foundDomainID int64
	for _, pd := range resp.Msg.PlatformDomains {
		if strings.HasSuffix(cfg.DomainConfig.Hostname, pd.Domain) {
			foundDomainID = pd.Id
			slog.Info("matched platform domain", "hostname", cfg.DomainConfig.Hostname, "platform_domain", pd.Domain, "id", pd.Id)
			break
		}
	}

	if foundDomainID == 0 {
		// Interactive selection as fallback
		options := make([]ui.SelectOption, len(resp.Msg.PlatformDomains))
		for i, domain := range resp.Msg.PlatformDomains {
			options[i] = ui.SelectOption{
				Label:       domain.Domain,
				Description: fmt.Sprintf("ID: %d", domain.Id),
				Value:       domain.Id,
			}
		}

		selected, selErr := g.deps.SelectFromList("Select platform domain for your service", options)
		if selErr != nil {
			return nil, fmt.Errorf("domain selection cancelled: %w", selErr)
		}

		domainID, ok := selected.(int64)
		if !ok {
			return nil, fmt.Errorf("invalid domain ID: expected int64, got %T", selected)
		}
		foundDomainID = domainID
	}

	return &domainv1.DomainInput{
		DomainSource:     domainv1.DomainType_DOMAIN_TYPE_PLATFORM_PROVIDED,
		Subdomain:        &subdomain,
		PlatformDomainId: &foundDomainID,
	}, nil
}
