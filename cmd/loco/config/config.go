package config

import (
	"fmt"

	"github.com/team-loco/loco/cmd/internal/host"

	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/session"
)

// configKey describes one key of ~/.loco/config.yaml: where it lives in the
// session config and the value used while it is unset.
type configKey struct {
	name  string
	field func(*session.SessionConfig) *string
	def   string
}

// configKeys is in the order `config list` prints.
var configKeys = []configKey{
	{"locoHost", func(c *session.SessionConfig) *string { return &c.LocoHost }, host.DefaultLocoHost},
	{"webHost", func(c *session.SessionConfig) *string { return &c.WebHost }, host.DefaultWebHost},
}

func lookupKey(name string) (configKey, error) {
	for _, k := range configKeys {
		if k.name == name {
			return k, nil
		}
	}
	names := make([]string, len(configKeys))
	for i, k := range configKeys {
		names[i] = k.name
	}
	return configKey{}, fmt.Errorf("unknown key %q — valid keys: %v", name, names)
}

func BuildConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage loco CLI configuration",
		Long:  "Get, set, and unset CLI configuration values stored in ~/.loco/config.yaml.",
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
