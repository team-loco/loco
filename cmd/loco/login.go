package main

import (
	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/host"
	"github.com/team-loco/loco/cmd/internal/login"
)

func newLoginCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to Loco",
		RunE: func(cmd *cobra.Command, _ []string) error {
			apiHost, err := host.GetHost(cmd)
			if err != nil {
				return err
			}
			webHost, err := host.GetWebHost(cmd)
			if err != nil {
				return err
			}
			device, err := cmd.Flags().GetBool("device")
			if err != nil {
				return err
			}
			store, err := env.Tokens()
			if err != nil {
				return err
			}
			return login.Run(cmd.Context(), login.Options{
				Command: "loco login", APIHost: apiHost, WebHost: webHost, Device: device, Store: store,
			})
		},
	}
	cmd.Flags().String("host", "", "Set the host URL")
	login.AddFlags(cmd)
	cmd.Flags().String("web-host", "", "Web UI URL that approves the sign-in")
	return cmd
}
