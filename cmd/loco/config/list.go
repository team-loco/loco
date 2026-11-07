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
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := session.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			for _, k := range configKeys {
				v := *k.field(cfg)
				if v == "" {
					v = k.def + " (default)"
				}
				fmt.Printf("%-8s = %s\n", k.name, v)
			}

			return nil
		},
	}
}
