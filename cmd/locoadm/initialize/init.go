package initialize

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/gitops"
)

func Command() *cobra.Command {
	var options gitops.InitOptions
	command := &cobra.Command{
		Use:   "init",
		Short: "Scaffold an operator GitOps repository with encrypted bootstrap Secrets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := gitops.Init(cmd.Context(), options); err != nil {
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "GitOps repository written to", options.Output)
			return err
		},
	}
	flags := []struct {
		name  string
		value *string
		help  string
	}{
		{"output", &options.Output, "New directory for the operator repository"},
		{"api", &options.API, "Public API URL"},
		{"web", &options.Web, "Public dashboard URL"},
		{"domain", &options.Domain, "Worker application domain"},
		{"region", &options.Region, "Worker region"},
		{"admin", &options.Admin, "First system administrator's verified email"},
		{"bundle", &options.Bundle, "Platform OCI reference pinned by SHA-256 digest"},
		{"api-env-file", &options.APIEnvFile, "Environment file to encrypt as the API Secret"},
		{"control-plane-recipient", &options.ControlPlaneRecipient, "Control-plane age public recipient"},
		{"worker-recipient", &options.WorkerRecipient, "Worker age public recipient"},
		{"admin-recipient", &options.AdminRecipient, "Offline administrator age public recipient"},
	}
	for _, flag := range flags {
		command.Flags().StringVar(flag.value, flag.name, "", flag.help)
		if err := command.MarkFlagRequired(flag.name); err != nil {
			panic(err)
		}
	}
	return command
}
