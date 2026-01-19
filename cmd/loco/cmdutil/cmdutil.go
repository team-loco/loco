package cmdutil

import (
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"time"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/keychain"
)

const LocoProdHost = "https://loco.deploy-app.com"

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

// LogRequestID logs the request ID from an error response for debugging.
func LogRequestID(err error) {
	// TODO: Extract request ID from connect error metadata if available
	slog.Debug("request failed", "error", err)
}
