package internal

import (
	"os"

	"github.com/spf13/cobra"
)

const locoProdHost = "https://loco.deploy-app.com"

// GetHost returns the API host from flag, env var, or default.
func GetHost(cmd *cobra.Command) (string, error) {
	host, err := cmd.Flags().GetString("host")
	if err != nil {
		return "", err
	}
	if host != "" {
		return host, nil
	}

	if envHost := os.Getenv("LOCO_HOST"); envHost != "" {
		return envHost, nil
	}

	return locoProdHost, nil
}
