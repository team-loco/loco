package resource

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
	domainv1 "github.com/team-loco/loco/proto/loco/domain/v1"
	"github.com/team-loco/loco/proto/loco/domain/v1/domainv1connect"
)

// resolveOrg resolves organization name from flag > env > config.
func resolveOrg(cmd *cobra.Command, loadConfig func() (*session.SessionConfig, error)) (string, error) {
	org, err := cmd.Flags().GetString("org")
	if err != nil {
		return "", fmt.Errorf("error reading org flag: %w", err)
	}
	if org != "" {
		slog.Debug("using org from flag")
		return org, nil
	}

	org = os.Getenv("LOCO__ORG")
	if org != "" {
		slog.Debug("using org from environment variable")
		return org, nil
	}

	cfg, err := loadConfig()
	if err != nil {
		slog.Debug("failed to load default config", "error", err)
		return "", fmt.Errorf("org not specified and no default found. Use --org flag or set LOCO__ORG environment variable")
	}

	scope, err := cfg.GetScope()
	if err == nil {
		slog.Debug("using org from default config")
		return scope.Organization.Name, nil
	}

	return "", fmt.Errorf("org not specified and no default found. Use --org flag or set LOCO__ORG environment variable")
}

// resolveWorkspace resolves workspace name from flag > env > config.
func resolveWorkspace(cmd *cobra.Command, loadConfig func() (*session.SessionConfig, error)) (string, error) {
	workspace, err := cmd.Flags().GetString("workspace")
	if err != nil {
		return "", fmt.Errorf("error reading workspace flag: %w", err)
	}
	if workspace != "" {
		slog.Debug("using workspace from flag")
		return workspace, nil
	}

	workspace = os.Getenv("LOCO__WORKSPACE")
	if workspace != "" {
		slog.Debug("using workspace from environment variable")
		return workspace, nil
	}

	cfg, err := loadConfig()
	if err != nil {
		slog.Debug("failed to load default config", "error", err)
		return "", fmt.Errorf("workspace not specified and no default found. Use --workspace flag or set LOCO__WORKSPACE environment variable")
	}

	scope, err := cfg.GetScope()
	if err == nil {
		slog.Debug("using workspace from default config")
		return scope.Workspace.Name, nil
	}

	return "", fmt.Errorf("workspace not specified and no default found. Use --workspace flag or set LOCO__WORKSPACE environment variable")
}

// resolveOrgID resolves organization ID, first checking config cache then API.
func resolveOrgID(ctx context.Context, cmd *cobra.Command, loadConfig func() (*session.SessionConfig, error), apiClient *client.Client) (string, error) {
	cfg, err := loadConfig()
	if err != nil {
		slog.Debug("failed to load config", "error", err)
		return "", fmt.Errorf("failed to load config: %w", err)
	}

	orgName, err := resolveOrg(cmd, loadConfig)
	if err != nil {
		return "", err
	}

	scope, err := cfg.GetScope()
	if err == nil && orgName == scope.Organization.Name {
		return scope.Organization.ID, nil
	}

	// Fall back to API lookup
	if apiClient == nil {
		return "", fmt.Errorf("login required - please run 'loco login'")
	}

	currentUser, err := apiClient.GetCurrentUser(ctx)
	if err != nil {
		slog.Debug("failed to get current user", "error", err)
		return "", fmt.Errorf("failed to get current user: %w", err)
	}

	orgs, err := apiClient.GetCurrentUserOrgs(ctx, currentUser.Id)
	if err != nil {
		slog.Debug("failed to get organizations", "error", err)
		return "", fmt.Errorf("failed to get organizations: %w", err)
	}

	for _, org := range orgs {
		if org.Name == orgName {
			slog.Debug("found org id from api", "orgId", org.Id)
			return org.Id, nil
		}
	}

	return "", fmt.Errorf("organization '%s' not found", orgName)
}

// resolveWorkspaceID resolves workspace ID, first checking config cache then API.
func resolveWorkspaceID(ctx context.Context, cmd *cobra.Command, loadConfig func() (*session.SessionConfig, error), apiClient *client.Client) (string, error) {
	cfg, err := loadConfig()
	if err != nil {
		slog.Debug("failed to load config", "error", err)
		return "", fmt.Errorf("failed to load config: %w", err)
	}

	workspaceName, err := resolveWorkspace(cmd, loadConfig)
	if err != nil {
		return "", err
	}

	scope, err := cfg.GetScope()
	if err == nil && workspaceName == scope.Workspace.Name {
		return scope.Workspace.ID, nil
	}

	// Fall back to API lookup - need org ID first
	orgID, err := resolveOrgID(ctx, cmd, loadConfig, apiClient)
	if err != nil {
		return "", err
	}

	if apiClient == nil {
		return "", fmt.Errorf("login required - please run 'loco login'")
	}

	currentUser, err := apiClient.GetCurrentUser(ctx)
	if err != nil {
		slog.Debug("failed to get current user", "error", err)
		return "", fmt.Errorf("failed to get current user: %w", err)
	}

	workspaces, err := apiClient.GetUserWorkspaces(ctx, currentUser.Id)
	if err != nil {
		slog.Debug("failed to get workspaces", "error", err)
		return "", fmt.Errorf("failed to get workspaces: %w", err)
	}

	for _, ws := range workspaces {
		if ws.Name == workspaceName && ws.OrgId == orgID {
			slog.Debug("found workspace id from api", "workspaceId", ws.Id)
			return ws.Id, nil
		}
	}

	return "", fmt.Errorf("workspace '%s' not found in organization", workspaceName)
}

// resolveDomainInput creates the domain input for resource creation.
// Only platform domains are supported.
func resolveDomainInput(
	ctx context.Context,
	domainClient domainv1connect.DomainServiceClient,
	selectFromList func(title string, options []ui.SelectOption) (any, error),
	authHeader string,
	cfg *config.LocoConfig,
) (*domainv1.DomainInput, error) {
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
	for _, pd := range resp.Msg.PlatformDomains {
		if strings.HasSuffix(cfg.DomainConfig.Hostname, pd.Domain) {
			foundDomainID = pd.Id
			slog.Info("matched platform domain", "hostname", cfg.DomainConfig.Hostname, "platform_domain", pd.Domain, "id", pd.Id)
			break
		}
	}

	if foundDomainID == "" {
		// Interactive selection as fallback
		options := make([]ui.SelectOption, len(resp.Msg.PlatformDomains))
		for i, domain := range resp.Msg.PlatformDomains {
			options[i] = ui.SelectOption{
				Label:       domain.Domain,
				Description: fmt.Sprintf("ID: %s", domain.Id),
				Value:       domain.Id,
			}
		}

		selected, selErr := selectFromList("Select platform domain for your service", options)
		if selErr != nil {
			return nil, fmt.Errorf("domain selection cancelled: %w", selErr)
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
