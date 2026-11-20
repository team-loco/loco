package secret

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
)

func newUnsetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unset KEY [KEY...]",
		Short: "Remove an environment's secrets by name",
		Long: `Remove one or more secrets from an environment. The API refuses a secret that the latest
deployment of a service reads, and names the services; remove the reference from their variables
and deploy them first.

Examples:
  loco secret unset --env production API_KEY LOG_LEVEL`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			t, err := resolveEnvironment(ctx, cmd)
			if err != nil {
				return err
			}
			req := &secretv1.DeleteSecretsRequest{
				EnvironmentId: t.Environment.GetId(),
				Names:         args,
			}

			resp, err := t.Clients.Secret.DeleteSecrets(ctx, req)
			if err != nil {
				return fmt.Errorf("unset secrets: %w", err)
			}
			names := strings.Join(args, ", ")
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s (revision %d)\n", names, resp.GetRevision())
			return nil
		},
	}
	addTargetFlags(cmd)
	return cmd
}
