package resource

import (
	"github.com/spf13/cobra"
)

// BuildResourceCmd creates the "resource" parent command with all subcommands.
func BuildResourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resource",
		Short: "Manage resources",
		Long:  "Commands for deploying, scaling, and managing resources on Loco.",
	}

	// Add subcommands
	cmd.AddCommand(
		buildDeployCmd(),
		buildScaleCmd(),
		buildStatusCmd(),
		buildLogsCmd(),
		buildEventsCmd(),
		buildEnvCmd(),
		buildDestroyCmd(),
	)

	return cmd
}
