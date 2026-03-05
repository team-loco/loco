package config

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/session"
)

func buildSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Args:  cobra.ExactArgs(2),
		Example: `  loco config set locoHost https://loco.example.com
  loco config set defaultAppDomain mycompany.app`,
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]

			cfg, err := session.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			switch key {
			case "locoHost":
				cfg.LocoHost = value
			case "defaultAppDomain":
				cfg.DefaultAppDomain = value
			default:
				return fmt.Errorf("unknown key %q — valid keys: %v", key, validKeys)
			}

			if err := cfg.Save(); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			fmt.Printf("%s = %s\n", key, value)
			return nil
		},
	}
}
