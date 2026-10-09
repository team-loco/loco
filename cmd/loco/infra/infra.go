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
  loco infra validate deploy/loco.yaml

  # Show what applying the file to the staging environment would change
  loco infra plan --env staging

  # Apply the file to the staging environment without a prompt
  loco infra apply --env staging --yes`,
	}

	cmd.AddCommand(
		buildInitCmd(),
		buildValidateCmd(),
		buildPlanCmd(),
		buildApplyCmd(),
	)

	return cmd
}
