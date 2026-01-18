package internal

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/user"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/docker"
	"github.com/team-loco/loco/internal/keychain"
	"github.com/team-loco/loco/internal/ui"
	"github.com/team-loco/loco/shared"
	sharedconfig "github.com/team-loco/loco/shared/config"
	deploymentv1 "github.com/team-loco/loco/shared/proto/loco/deployment/v1"
	"github.com/team-loco/loco/shared/proto/loco/deployment/v1/deploymentv1connect"
	domainv1 "github.com/team-loco/loco/shared/proto/loco/domain/v1"
	"github.com/team-loco/loco/shared/proto/loco/domain/v1/domainv1connect"
	registryv1 "github.com/team-loco/loco/shared/proto/loco/registry/v1"
	"github.com/team-loco/loco/shared/proto/loco/registry/v1/registryv1connect"
	resourcev1 "github.com/team-loco/loco/shared/proto/loco/resource/v1"
	"github.com/team-loco/loco/shared/proto/loco/resource/v1/resourcev1connect"
)

const LocoProdHost = "https://loco.deploy-app.com"

// ServiceDeps contains all dependencies for service commands.
// Using function types allows easy testing via dependency injection.
type ServiceDeps struct {
	// I/O
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	// Session config loading (for org/workspace context)
	LoadSessionConfig func() (*config.SessionConfig, error)

	// Loco.toml config loading (optional for deploy)
	LoadLocoConfig func(path string) (*sharedconfig.LoadedConfig, error)

	// Auth
	GetLocoToken func(username string) (*keychain.UserToken, error)

	// Interactive prompts
	SelectFromList func(title string, options []ui.SelectOption) (any, error)
	AskForString   func(prompt string) (string, error)

	// API client (for convenience methods)
	APIClient *client.Client

	// Resource operations
	GetResource            func(ctx context.Context, req *connect.Request[resourcev1.GetResourceRequest]) (*connect.Response[resourcev1.GetResourceResponse], error)
	CreateResource         func(ctx context.Context, req *connect.Request[resourcev1.CreateResourceRequest]) (*connect.Response[resourcev1.CreateResourceResponse], error)
	DeleteResource         func(ctx context.Context, req *connect.Request[resourcev1.DeleteResourceRequest]) (*connect.Response[resourcev1.DeleteResourceResponse], error)
	ScaleResource          func(ctx context.Context, req *connect.Request[resourcev1.ScaleResourceRequest]) (*connect.Response[resourcev1.ScaleResourceResponse], error)
	UpdateResourceEnv      func(ctx context.Context, req *connect.Request[resourcev1.UpdateResourceEnvRequest]) (*connect.Response[resourcev1.UpdateResourceEnvResponse], error)
	GetResourceStatus      func(ctx context.Context, req *connect.Request[resourcev1.GetResourceStatusRequest]) (*connect.Response[resourcev1.GetResourceStatusResponse], error)
	ListWorkspaceResources func(ctx context.Context, req *connect.Request[resourcev1.ListWorkspaceResourcesRequest]) (*connect.Response[resourcev1.ListWorkspaceResourcesResponse], error)
	ListRegions            func(ctx context.Context, req *connect.Request[resourcev1.ListRegionsRequest]) (*connect.Response[resourcev1.ListRegionsResponse], error)
	WatchLogs              func(ctx context.Context, req *connect.Request[resourcev1.WatchLogsRequest]) (*connect.ServerStreamForClient[resourcev1.WatchLogsResponse], error)
	ListResourceEvents     func(ctx context.Context, req *connect.Request[resourcev1.ListResourceEventsRequest]) (*connect.Response[resourcev1.ListResourceEventsResponse], error)

	// Deployment operations
	CreateDeployment func(ctx context.Context, req *connect.Request[deploymentv1.CreateDeploymentRequest]) (*connect.Response[deploymentv1.CreateDeploymentResponse], error)
	WatchDeployment  func(ctx context.Context, req *connect.Request[deploymentv1.WatchDeploymentRequest]) (*connect.ServerStreamForClient[deploymentv1.WatchDeploymentResponse], error)

	// Domain operations
	ListPlatformDomains func(ctx context.Context, req *connect.Request[domainv1.ListPlatformDomainsRequest]) (*connect.Response[domainv1.ListPlatformDomainsResponse], error)

	// Registry operations
	GetGitlabToken func(ctx context.Context, req *connect.Request[registryv1.GetGitlabTokenRequest]) (*connect.Response[registryv1.GetGitlabTokenResponse], error)

	// Docker client factory
	NewDockerClient func(cfg *sharedconfig.LoadedConfig) (*docker.DockerClient, error)

	// Cached values (set during initialization)
	host  string
	token string
}

// NewServiceDeps creates ServiceDeps with production implementations.
func NewServiceDeps(host, token string) *ServiceDeps {
	httpClient := shared.NewHTTPClient()
	resourceClient := resourcev1connect.NewResourceServiceClient(httpClient, host)
	deploymentClient := deploymentv1connect.NewDeploymentServiceClient(httpClient, host)
	domainClient := domainv1connect.NewDomainServiceClient(httpClient, host)
	registryClient := registryv1connect.NewRegistryServiceClient(httpClient, host)

	apiClient := client.NewClient(host, token)

	return &ServiceDeps{
		Stdout:            os.Stdout,
		Stderr:            os.Stderr,
		Stdin:             os.Stdin,
		LoadSessionConfig: config.Load,
		LoadLocoConfig:    sharedconfig.Load,
		GetLocoToken:      keychain.GetLocoToken,
		SelectFromList:    ui.SelectFromList,
		AskForString:      ui.AskForString,

		APIClient: apiClient,

		// Resource operations
		GetResource:            resourceClient.GetResource,
		CreateResource:         resourceClient.CreateResource,
		DeleteResource:         resourceClient.DeleteResource,
		ScaleResource:          resourceClient.ScaleResource,
		UpdateResourceEnv:      resourceClient.UpdateResourceEnv,
		GetResourceStatus:      resourceClient.GetResourceStatus,
		ListWorkspaceResources: resourceClient.ListWorkspaceResources,
		ListRegions:            resourceClient.ListRegions,
		WatchLogs:              resourceClient.WatchLogs,
		ListResourceEvents:     resourceClient.ListResourceEvents,

		// Deployment operations
		CreateDeployment: deploymentClient.CreateDeployment,
		WatchDeployment:  deploymentClient.WatchDeployment,

		// Domain operations
		ListPlatformDomains: domainClient.ListPlatformDomains,

		// Registry operations
		GetGitlabToken: registryClient.GetGitlabToken,

		// Docker
		NewDockerClient: docker.NewClient,

		host:  host,
		token: token,
	}
}

// Host returns the API host.
func (d *ServiceDeps) Host() string {
	return d.host
}

// Token returns the auth token.
func (d *ServiceDeps) Token() string {
	return d.token
}

// AuthHeader returns the authorization header value.
func (d *ServiceDeps) AuthHeader() string {
	return fmt.Sprintf("Bearer %s", d.token)
}

// GetHost resolves the API host from flag > env > default.
func GetHost(cmd *cobra.Command) (string, error) {
	host, err := cmd.Flags().GetString("host")
	if err != nil {
		return "", fmt.Errorf("error reading host flag: %w", err)
	}
	if host != "" {
		slog.Debug("using host from flag")
		return host, nil
	}

	host = os.Getenv("LOCO__HOST")
	if host != "" {
		slog.Debug("using host from environment variable")
		return host, nil
	}

	slog.Debug("defaulting to prod url")
	return LocoProdHost, nil
}

// GetCurrentLocoToken retrieves the token for the current OS user.
func GetCurrentLocoToken() (*keychain.UserToken, error) {
	usr, err := user.Current()
	if err != nil {
		slog.Debug("failed to get current user", "error", err)
		return nil, err
	}
	locoToken, err := keychain.GetLocoToken(usr.Name)
	if err != nil {
		slog.Debug("failed to get loco token", "error", err)
		return nil, err
	}

	if locoToken.ExpiresAt.Before(time.Now().Add(5 * time.Minute)) {
		slog.Debug("token is expired or will expire soon", "expires_at", locoToken.ExpiresAt)
		return nil, fmt.Errorf("token is expired or will expire soon. Please re-login via `loco login`")
	}

	return locoToken, nil
}

// ContextResolver resolves org/workspace from flags, env, or session config.
type ContextResolver struct {
	deps *ServiceDeps
}

// NewContextResolver creates a new ContextResolver.
func NewContextResolver(deps *ServiceDeps) *ContextResolver {
	return &ContextResolver{deps: deps}
}

// ResolveOrg resolves organization name from flag > env > config.
func (r *ContextResolver) ResolveOrg(cmd *cobra.Command) (string, error) {
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

	cfg, err := r.deps.LoadSessionConfig()
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

// ResolveWorkspace resolves workspace name from flag > env > config.
func (r *ContextResolver) ResolveWorkspace(cmd *cobra.Command) (string, error) {
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

	cfg, err := r.deps.LoadSessionConfig()
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

// ResolveOrgID resolves organization ID, first checking config cache then API.
func (r *ContextResolver) ResolveOrgID(ctx context.Context, cmd *cobra.Command) (int64, error) {
	cfg, err := r.deps.LoadSessionConfig()
	if err != nil {
		slog.Debug("failed to load config", "error", err)
		return 0, fmt.Errorf("failed to load config: %w", err)
	}

	orgName, err := r.ResolveOrg(cmd)
	if err != nil {
		return 0, err
	}

	scope, err := cfg.GetScope()
	if err == nil && orgName == scope.Organization.Name {
		return scope.Organization.ID, nil
	}

	// Fall back to API lookup
	currentUser, err := r.deps.APIClient.GetCurrentUser(ctx)
	if err != nil {
		slog.Debug("failed to get current user", "error", err)
		return 0, fmt.Errorf("failed to get current user: %w", err)
	}

	orgs, err := r.deps.APIClient.GetCurrentUserOrgs(ctx, currentUser.Id)
	if err != nil {
		slog.Debug("failed to get organizations", "error", err)
		return 0, fmt.Errorf("failed to get organizations: %w", err)
	}

	for _, org := range orgs {
		if org.Name == orgName {
			slog.Debug("found org id from api", "orgId", org.Id)
			return org.Id, nil
		}
	}

	return 0, fmt.Errorf("organization '%s' not found", orgName)
}

// ResolveWorkspaceID resolves workspace ID, first checking config cache then API.
func (r *ContextResolver) ResolveWorkspaceID(ctx context.Context, cmd *cobra.Command, orgID int64) (int64, error) {
	cfg, err := r.deps.LoadSessionConfig()
	if err != nil {
		slog.Debug("failed to load config", "error", err)
		return 0, fmt.Errorf("failed to load config: %w", err)
	}

	workspaceName, err := r.ResolveWorkspace(cmd)
	if err != nil {
		return 0, err
	}

	scope, err := cfg.GetScope()
	if err == nil && workspaceName == scope.Workspace.Name {
		return scope.Workspace.ID, nil
	}

	// Fall back to API lookup
	currentUser, err := r.deps.APIClient.GetCurrentUser(ctx)
	if err != nil {
		slog.Debug("failed to get current user", "error", err)
		return 0, fmt.Errorf("failed to get current user: %w", err)
	}

	workspaces, err := r.deps.APIClient.GetUserWorkspaces(ctx, currentUser.Id)
	if err != nil {
		slog.Debug("failed to get workspaces", "error", err)
		return 0, fmt.Errorf("failed to get workspaces: %w", err)
	}

	for _, ws := range workspaces {
		if ws.Name == workspaceName && ws.OrgId == orgID {
			slog.Debug("found workspace id from api", "workspaceId", ws.Id)
			return ws.Id, nil
		}
	}

	return 0, fmt.Errorf("workspace '%s' not found in organization", workspaceName)
}

// ResolveResourceByName finds a resource by name in the current workspace.
func (r *ContextResolver) ResolveResourceByName(ctx context.Context, cmd *cobra.Command, name string) (*resourcev1.Resource, error) {
	orgID, err := r.ResolveOrgID(ctx, cmd)
	if err != nil {
		return nil, err
	}

	workspaceID, err := r.ResolveWorkspaceID(ctx, cmd, orgID)
	if err != nil {
		return nil, err
	}

	req := connect.NewRequest(&resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_NameKey{
			NameKey: &resourcev1.GetResourceNameKey{
				WorkspaceId: workspaceID,
				Name:        name,
			},
		},
	})
	req.Header().Set("Authorization", r.deps.AuthHeader())

	resp, err := r.deps.GetResource(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("service '%s' not found: %w", name, err)
	}

	return resp.Msg.Resource, nil
}
