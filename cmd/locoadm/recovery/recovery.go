package recovery

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/installation"
)

const exportTimeout = 5 * time.Minute

func Command() *cobra.Command {
	command := &cobra.Command{Use: "recovery", Short: "Export encrypted installation recovery material"}
	var file string
	var options installation.RecoveryExport
	export := &cobra.Command{
		Use: "export", Short: "Encrypt seal material and recovery shares for separate custodians", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			config, err := installation.Load(file)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), exportTimeout)
			defer cancel()
			if exportErr := installation.ExportRecovery(ctx, *config, options); exportErr != nil {
				return exportErr
			}
			_, err = fmt.Fprintln(
				cmd.OutOrStdout(),
				"Encrypted recovery export complete. Distribute files to their custodians. "+
					"Verify restoration before removing sources.",
			)
			return err
		}}
	export.Flags().StringVar(&file, "file", "installation.yaml", "Installation configuration file")
	export.Flags().StringVar(&options.Output, "output", "", "New directory for encrypted recovery files")
	export.Flags().
		StringVar(&options.Recipient, "recipient", "", "Age X25519 recipient for seal material and the generate-root token")
	export.Flags().StringArrayVar(&options.ShareRecipients, "share-recipient", nil,
		"Distinct age X25519 recipient for each share; repeat in share order",
	)
	for _, flag := range []string{"output", "recipient", "share-recipient"} {
		if err := export.MarkFlagRequired(flag); err != nil {
			panic(err)
		}
	}
	command.AddCommand(export)
	return command
}
