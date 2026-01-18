package loco

import (
	"fmt"

	"github.com/spf13/cobra"
)

// getLocoTomlPath returns the loco.toml path from the --config flag, defaulting to "loco.toml".
func getLocoTomlPath(cmd *cobra.Command) (string, error) {
	configPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return "", fmt.Errorf("error reading config flag: %w", err)
	}

	if configPath == "" {
		configPath = "loco.toml"
	}

	return configPath, nil
}
