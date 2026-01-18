package loco

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

const locoProdHost = "https://loco.deploy-app.com"

// getHost resolves the API host from flag > env > default.
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
