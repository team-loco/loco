package loco

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/keychain"
)

const locoProdHost = "https://loco.deploy-app.com"

func getHost(cmd *cobra.Command) (string, error) {
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
	return locoProdHost, nil
}

func getLocoToken() (*keychain.UserToken, error) {
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

	return locoToken, err
}

func parseLocoTomlPath(cmd *cobra.Command) (string, error) {
	configPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return "", fmt.Errorf("error reading config flag: %w", err)
	}
	if configPath == "" {
		return "loco.toml", nil
	}
	return configPath, nil
}

func parseImageId(cmd *cobra.Command) (string, error) {
	imageId, err := cmd.Flags().GetString("image")
	if err != nil {
		return "", fmt.Errorf("error reading image flag: %w", err)
	}
	return imageId, nil
}

func getOrg(cmd *cobra.Command) (string, error) {
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

	cfg, err := config.Load()
	if err != nil {
		slog.Debug("failed to load default config", "error", err)
		return "", fmt.Errorf("org not specified and no default found. Use -o/--org flag or set LOCO__ORG environment variable")
	}

	scope, err := cfg.GetScope()
	if err == nil {
		slog.Debug("using org from default config")
		return scope.Organization.Name, nil
	}

	return "", fmt.Errorf("org not specified and no default found. Use -o/--org flag or set LOCO__ORG environment variable")
}

func getWorkspace(cmd *cobra.Command) (string, error) {
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

	cfg, err := config.Load()
	if err != nil {
		slog.Debug("failed to load default config", "error", err)
		return "", fmt.Errorf("workspace not specified and no default found. Use -w/--workspace flag or set LOCO__WORKSPACE environment variable")
	}

	scope, err := cfg.GetScope()
	if err == nil {
		slog.Debug("using workspace from default config")
		return scope.Workspace.Name, nil
	}

	return "", fmt.Errorf("workspace not specified and no default found. Use -w/--workspace flag or set LOCO__WORKSPACE environment variable")
}

// todo: lots wrong here, we can potentially pass down clients, not-reload config a thousand times.
// todo: pass down command ctx.
func getOrgId(cmd *cobra.Command) (int64, error) {
	cfg, err := config.Load()
	if err != nil {
		slog.Debug("failed to load config", "error", err)
		return 0, fmt.Errorf("failed to load config: %w", err)
	}

	orgName, err := getOrg(cmd)
	if err != nil {
		return 0, err
	}

	scope, err := cfg.GetScope()
	if err == nil && orgName == scope.Organization.Name {
		return scope.Organization.ID, nil
	}

	host, err := getHost(cmd)
	if err != nil {
		return 0, err
	}

	locoToken, err := getLocoToken()
	if err != nil {
		return 0, err
	}

	apiClient := client.NewClient(host, locoToken.Token)
	orgs, err := apiClient.GetCurrentUserOrgs(context.Background())
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

func getWorkspaceId(cmd *cobra.Command) (int64, error) {
	cfg, err := config.Load()
	if err != nil {
		slog.Debug("failed to load config", "error", err)
		return 0, fmt.Errorf("failed to load config: %w", err)
	}

	workspaceName, err := getWorkspace(cmd)
	if err != nil {
		return 0, err
	}

	scope, err := cfg.GetScope()
	if err == nil && workspaceName == scope.Workspace.Name {
		return scope.Workspace.ID, nil
	}

	orgId, err := getOrgId(cmd)
	if err != nil {
		return 0, err
	}

	host, err := getHost(cmd)
	if err != nil {
		return 0, err
	}

	locoToken, err := getLocoToken()
	if err != nil {
		return 0, err
	}

	apiClient := client.NewClient(host, locoToken.Token)
	workspaces, err := apiClient.GetUserWorkspaces(context.Background())
	if err != nil {
		slog.Debug("failed to get workspaces", "error", err)
		return 0, fmt.Errorf("failed to get workspaces: %w", err)
	}

	for _, ws := range workspaces {
		if ws.Name == workspaceName && ws.OrgId == orgId {
			slog.Debug("found workspace id from api", "workspaceId", ws.Id)
			return ws.Id, nil
		}
	}

	return 0, fmt.Errorf("workspace '%s' not found in organization", workspaceName)
}

// logRequestID extracts and logs the X-Loco-Request-Id only if err is not nil
func logRequestID(ctx context.Context, err error, msg string) {
	if err == nil {
		return
	}

	const requestIDHeaderName = "X-Loco-Request-Id"
	var headerValue string
	var cErr *connect.Error

	if errors.As(err, &cErr) {
		headerValue = cErr.Meta().Get(requestIDHeaderName)
	}

	slog.ErrorContext(ctx, msg, requestIDHeaderName, headerValue, "error", err)
}
