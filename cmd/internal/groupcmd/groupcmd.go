// Package groupcmd makes command groups reject unknown subcommands.
package groupcmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// RejectUnknown gives every non-root command that has subcommands but no
// Run of its own a RunE that prints help without arguments and fails with an
// unknown command error otherwise. Cobra lets such groups accept any
// arguments and print their help with exit status 0.
func RejectUnknown(root *cobra.Command) {
	for _, cmd := range root.Commands() {
		RejectUnknown(cmd)
		if cmd.HasSubCommands() && !cmd.Runnable() {
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				if len(args) > 0 {
					return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
				}
				return cmd.Help()
			}
		}
	}
}
