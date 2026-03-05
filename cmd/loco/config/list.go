package config

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/session"
)

func buildListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List all configuration values",
		Args:    cobra.NoArgs,
		Example: `  loco config list`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := session.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			locoHost := cfg.LocoHost
			if locoHost == "" {
				locoHost = "https://loco.build (default)"
			}

			defaultAppDomain := cfg.DefaultAppDomain
			if defaultAppDomain == "" {
				defaultAppDomain = "onloco.app (default)"
			}

			fmt.Printf("locoHost         = %s\n", locoHost)
			fmt.Printf("defaultAppDomain = %s\n", defaultAppDomain)

			return nil
		},
	}
}
