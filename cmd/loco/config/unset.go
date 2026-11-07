package config

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/session"
)

func buildUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "unset <key>",
		Short:   "Unset a configuration value, reverting to the default",
		Args:    cobra.ExactArgs(1),
		Example: `  loco config unset locoHost`,
		RunE: func(_ *cobra.Command, args []string) error {
			key := args[0]

			cfg, err := session.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			k, err := lookupKey(key)
			if err != nil {
				return err
			}
			*k.field(cfg) = ""

			if err := cfg.Save(); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			fmt.Printf("%s unset\n", key)
			return nil
		},
	}
}
