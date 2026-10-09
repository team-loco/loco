package resource

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	"github.com/team-loco/loco/gen/go/loco/domain/v1/domainv1connect"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/ui"
)

// resolveDomainInput creates the domain input for resource creation.
// Returns nil if no DomainConfig is set (app will not receive internet traffic).
// Only platform domains are supported.
func resolveDomainInput(
	ctx context.Context,
	domainClient domainv1connect.DomainServiceClient,
	selectFromList func(title string, options []ui.SelectOption) (any, error),
	authHeader string,
	cfg *config.LocoConfig,
) (*domainv1.DomainInput, error) {
	if cfg.DomainConfig == nil {
		return nil, nil
	}

	if cfg.DomainConfig.Type == "custom" {
		return nil, errors.New("custom domains are not supported - please use a platform domain")
	}

	subdomain := config.ExtractSubdomainFromHostname(cfg.DomainConfig.Hostname)
	if subdomain == "" {
		return nil, errors.New("failed to extract subdomain from hostname")
	}

	activeOnly := true
	req := connect.NewRequest(&domainv1.ListPlatformDomainsRequest{
		ActiveOnly: &activeOnly,
	})
	req.Header().Set("Authorization", authHeader)

	resp, err := domainClient.ListPlatformDomains(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch platform domains: %w", err)
	}

	// Find matching platform domain
	var foundDomainID string
	for _, pd := range resp.Msg.GetPlatformDomains() {
		if strings.HasSuffix(cfg.DomainConfig.Hostname, pd.GetDomain()) {
			foundDomainID = pd.GetId()
			slog.Info(
				"matched platform domain",
				"hostname",
				cfg.DomainConfig.Hostname,
				"platform_domain",
				pd.GetDomain(),
				"id",
				pd.GetId(),
			)
			break
		}
	}

	if foundDomainID == "" {
		// Interactive selection as fallback
		options := make([]ui.SelectOption, len(resp.Msg.GetPlatformDomains()))
		for i, domain := range resp.Msg.GetPlatformDomains() {
			options[i] = ui.SelectOption{
				Label:       domain.GetDomain(),
				Description: fmt.Sprintf("ID: %s", domain.GetId()),
				Value:       domain.GetId(),
			}
		}

		selected, selErr := selectFromList("Select platform domain for your service", options)
		if selErr != nil {
			return nil, fmt.Errorf("domain selection canceled: %w", selErr)
		}

		domainID, ok := selected.(string)
		if !ok {
			return nil, fmt.Errorf("invalid domain ID: expected string, got %T", selected)
		}
		foundDomainID = domainID
	}

	return &domainv1.DomainInput{
		DomainSource:     domainv1.DomainType_DOMAIN_TYPE_PLATFORM_PROVIDED,
		Subdomain:        &subdomain,
		PlatformDomainId: &foundDomainID,
	}, nil
}
