package config

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/session"
)

func buildUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "unset <key>",
		Short:   "Unset a configuration value, reverting to the default",
		Args:    cobra.ExactArgs(1),
		Example: `  loco config unset locoHost`,
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]

			cfg, err := session.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			switch key {
			case "locoHost":
				cfg.LocoHost = ""
			case "defaultAppDomain":
				cfg.DefaultAppDomain = ""
			default:
				return fmt.Errorf("unknown key %q — valid keys: %v", key, validKeys)
			}

			if err := cfg.Save(); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			fmt.Printf("%s unset\n", key)
			return nil
		},
	}
}
