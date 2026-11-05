package validate

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/installation"
)

func BuildValidateCmd() *cobra.Command {
	var file string
	command := &cobra.Command{
		Use: "validate", Short: "Validate an installation file without accessing infrastructure",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := installation.Load(file); err != nil {
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "Installation configuration is valid.")
			return err
		},
	}
	command.Flags().StringVar(&file, "file", "installation.yaml", "Installation file")
	return command
}
