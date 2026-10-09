package infra

import (
	"github.com/spf13/cobra"
)

// BuildInfraCmd creates the "infra" parent command for everything that works on loco.yaml.
func BuildInfraCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "infra",
		Short: "Declare services in loco.yaml",
		Long:  "Commands that read the loco.yaml file declaring the services Loco runs for a workspace.",
		Example: `  # Write a starter loco.yaml for a service named api
  loco infra init --name api

  # Check a loco.yaml without calling the API
  loco infra validate

  # Check a file somewhere else
  loco infra validate deploy/loco.yaml`,
	}

	cmd.AddCommand(
		buildInitCmd(),
		buildValidateCmd(),
	)

	return cmd
}
