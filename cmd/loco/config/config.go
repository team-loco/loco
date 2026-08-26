package config

import (
	"github.com/spf13/cobra"
)

// validKeys lists all configurable keys in ~/.loco/config.toml.
var validKeys = []string{"locoHost", "defaultAppDomain"}

func BuildConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage loco CLI configuration",
		Long:  "Get, set, and unset CLI configuration values stored in ~/.loco/config.toml.",
		Example: `  # List all configuration values
  loco config list

  # Set the API host (e.g., for a self-hosted instance)
  loco config set locoHost https://selfhosted.loco.com

  # Get the current API host
  loco config get locoHost

  # Reset to default
  loco config unset locoHost`,
	}

	cmd.AddCommand(
		buildGetCmd(),
		buildSetCmd(),
		buildUnsetCmd(),
		buildListCmd(),
	)

	return cmd
}
