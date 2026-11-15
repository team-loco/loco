package login

import (
	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/installation"
	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/cmd/internal/login"
)

// Command signs in to the installation's API with the same flow and session store as loco login.
func Command() *cobra.Command {
	var file string
	command := &cobra.Command{
		Use:   "login",
		Short: "Sign in to the installation's API; the first sign-in of a configured admin makes it a system admin",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			config, err := installation.Load(file)
			if err != nil {
				return err
			}
			device, err := cmd.Flags().GetBool("device")
			if err != nil {
				return err
			}
			store, err := keychain.ForCurrentUser()
			if err != nil {
				return err
			}
			return login.Run(cmd.Context(), login.Options{
				Command: "locoadm login",
				APIHost: config.ControlPlane.APIURL,
				WebHost: config.ControlPlane.WebURL,
				Device:  device,
				Store:   store,
			})
		},
	}
	command.Flags().StringVar(&file, "file", installation.DefaultFile, installation.FileFlagUsage)
	login.AddFlags(command)
	return command
}
