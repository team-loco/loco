package cmdutil

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/session"
)

const defaultLocoHost = "https://loco.build"

// GetHost resolves the API host from flag > env > config file > default.
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

	cfg, err := session.Load()
	if err == nil && cfg.LocoHost != "" {
		slog.Debug("using host from config file")
		return cfg.LocoHost, nil
	}

	slog.Debug("defaulting to prod url")
	return defaultLocoHost, nil
}

// GetLocoTomlPath resolves the loco.toml path from flag or default.
func GetLocoTomlPath(cmd *cobra.Command) (string, error) {
	configPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return "", fmt.Errorf("error reading config flag: %w", err)
	}

	if configPath == "" {
		configPath = "loco.toml"
	}

	return configPath, nil
}
