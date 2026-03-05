package config

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/session"
)

func buildGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <key>",
		Short:   "Get a configuration value",
		Args:    cobra.ExactArgs(1),
		Example: `  loco config get locoHost`,
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]

			cfg, err := session.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			switch key {
			case "locoHost":
				if cfg.LocoHost != "" {
					fmt.Println(cfg.LocoHost)
				} else {
					fmt.Println("(not set, default: https://loco.build)")
				}
			case "defaultAppDomain":
				if cfg.DefaultAppDomain != "" {
					fmt.Println(cfg.DefaultAppDomain)
				} else {
					fmt.Println("(not set, default: onloco.app)")
				}
			default:
				return fmt.Errorf("unknown key %q — valid keys: %v", key, validKeys)
			}

			return nil
		},
	}
}
