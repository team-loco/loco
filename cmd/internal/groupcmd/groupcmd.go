// Package groupcmd makes command groups reject unknown subcommands.
package groupcmd

import "github.com/spf13/cobra"

// RejectUnknown makes every non-root command that has subcommands but no Run
// of its own print help without arguments and fail with an unknown command
// error otherwise. Cobra lets such groups accept any arguments and print their
// help with exit status 0.
func RejectUnknown(root *cobra.Command) {
	for _, cmd := range root.Commands() {
		RejectUnknown(cmd)
		if cmd.HasSubCommands() && !cmd.Runnable() {
			cmd.Args = cobra.NoArgs
			cmd.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
		}
	}
}
