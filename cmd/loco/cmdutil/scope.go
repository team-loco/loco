package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/session"
)

var (
	errOrgNotSpecified = errors.New(
		"org not specified and no default found. Use --org flag or set LOCO_ORG environment variable",
	)
	errWorkspaceNotSpecified = errors.New(
		"workspace not specified and no default found. Use --workspace flag or set LOCO_WORKSPACE environment variable",
	)
)

// ResolveOrg resolves organization name from flag > env > config.
func ResolveOrg(cmd *cobra.Command, loadConfig func() (*session.SessionConfig, error)) (string, error) {
	org, err := cmd.Flags().GetString("org")
	if err != nil {
		return "", fmt.Errorf("error reading org flag: %w", err)
	}
	if org != "" {
		slog.Debug("using org from flag")
		return org, nil
	}

	org = os.Getenv("LOCO_ORG")
	if org != "" {
		slog.Debug("using org from environment variable")
		return org, nil
	}

	cfg, err := loadConfig()
	if err != nil {
		slog.Debug("failed to load default config", "error", err)
		return "", errOrgNotSpecified
	}

	scope, err := cfg.GetScope()
	if err == nil {
		slog.Debug("using org from default config")
		return scope.Organization.Name, nil
	}

	return "", errOrgNotSpecified
}

// ResolveWorkspace resolves workspace name from flag > env > config.
func ResolveWorkspace(cmd *cobra.Command, loadConfig func() (*session.SessionConfig, error)) (string, error) {
	workspace, err := cmd.Flags().GetString("workspace")
	if err != nil {
		return "", fmt.Errorf("error reading workspace flag: %w", err)
	}
	if workspace != "" {
		slog.Debug("using workspace from flag")
		return workspace, nil
	}

	workspace = os.Getenv("LOCO_WORKSPACE")
	if workspace != "" {
		slog.Debug("using workspace from environment variable")
		return workspace, nil
	}

	cfg, err := loadConfig()
	if err != nil {
		slog.Debug("failed to load default config", "error", err)
		return "", errWorkspaceNotSpecified
	}

	scope, err := cfg.GetScope()
	if err == nil {
		slog.Debug("using workspace from default config")
		return scope.Workspace.Name, nil
	}

	return "", errWorkspaceNotSpecified
}

// ResolveOrgID resolves organization ID, first checking config cache then API.
func ResolveOrgID(
	ctx context.Context,
	cmd *cobra.Command,
	loadConfig func() (*session.SessionConfig, error),
	apiClient *client.Client,
) (string, error) {
	cfg, err := loadConfig()
	if err != nil {
		slog.Debug("failed to load config", "error", err)
		return "", fmt.Errorf("failed to load config: %w", err)
	}

	orgName, err := ResolveOrg(cmd, loadConfig)
	if err != nil {
		return "", err
	}

	scope, err := cfg.GetScope()
	if err == nil && orgName == scope.Organization.Name {
		return scope.Organization.ID, nil
	}

	if apiClient == nil {
		return "", ErrLoginRequired
	}

	currentUser, err := apiClient.GetCurrentUser(ctx)
	if err != nil {
		slog.Debug("failed to get current user", "error", err)
		return "", fmt.Errorf("failed to get current user: %w", err)
	}

	orgs, err := apiClient.GetCurrentUserOrgs(ctx, currentUser.GetId())
	if err != nil {
		slog.Debug("failed to get organizations", "error", err)
		return "", fmt.Errorf("failed to get organizations: %w", err)
	}

	for _, org := range orgs {
		if org.GetName() == orgName {
			slog.Debug("found org id from api", "orgId", org.GetId())
			return org.GetId(), nil
		}
	}

	return "", fmt.Errorf("organization '%s' not found", orgName)
}

// ResolveWorkspaceID resolves workspace ID, first checking config cache then API.
func ResolveWorkspaceID(
	ctx context.Context,
	cmd *cobra.Command,
	loadConfig func() (*session.SessionConfig, error),
	apiClient *client.Client,
) (string, error) {
	cfg, err := loadConfig()
	if err != nil {
		slog.Debug("failed to load config", "error", err)
		return "", fmt.Errorf("failed to load config: %w", err)
	}

	workspaceName, err := ResolveWorkspace(cmd, loadConfig)
	if err != nil {
		return "", err
	}

	orgName, err := ResolveOrg(cmd, loadConfig)
	if err != nil {
		return "", err
	}

	scope, err := cfg.GetScope()
	if err == nil && orgName == scope.Organization.Name && workspaceName == scope.Workspace.Name {
		return scope.Workspace.ID, nil
	}

	orgID, err := ResolveOrgID(ctx, cmd, loadConfig, apiClient)
	if err != nil {
		return "", err
	}

	if apiClient == nil {
		return "", ErrLoginRequired
	}

	currentUser, err := apiClient.GetCurrentUser(ctx)
	if err != nil {
		slog.Debug("failed to get current user", "error", err)
		return "", fmt.Errorf("failed to get current user: %w", err)
	}

	workspaces, err := apiClient.GetUserWorkspaces(ctx, currentUser.GetId())
	if err != nil {
		slog.Debug("failed to get workspaces", "error", err)
		return "", fmt.Errorf("failed to get workspaces: %w", err)
	}

	for _, ws := range workspaces {
		if ws.GetName() == workspaceName && ws.GetOrgId() == orgID {
			slog.Debug("found workspace id from api", "workspaceId", ws.GetId())
			return ws.GetId(), nil
		}
	}

	return "", fmt.Errorf("workspace '%s' not found in organization", workspaceName)
}
