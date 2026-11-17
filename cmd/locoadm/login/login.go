package login

import (
	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/cmd/internal/login"
)

// Command signs in to the installation's API with the same flow and session store as loco login.
func Command() *cobra.Command {
	var api, web string
	command := &cobra.Command{
		Use:   "login",
		Short: "Sign in to the installation's API; the first sign-in of a configured admin makes it a system admin",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
				APIHost: api,
				WebHost: web,
				Device:  device,
				Store:   store,
			})
		},
	}
	command.Flags().StringVar(&api, "api", "", "Control-plane API URL")
	command.Flags().StringVar(&web, "web", "", "Dashboard URL for sign-in")
	command.MarkFlagsRequiredTogether("api", "web")
	if err := command.MarkFlagRequired("api"); err != nil {
		panic(err)
	}
	login.AddFlags(command)
	return command
}
