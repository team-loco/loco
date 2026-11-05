package env

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
)

func newUnsetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unset KEY [KEY...]",
		Short: "Remove a service's secrets by name",
		Long: `Remove one or more secrets from a service in an environment. The API refuses a name that the
service's active deployment still declares; remove it from loco.yaml and apply first.

Examples:
  loco env unset --service web --env production API_KEY LOG_LEVEL`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			t, service, err := resolveService(ctx, cmd)
			if err != nil {
				return err
			}
			req := &secretv1.DeleteSecretsRequest{
				ResourceId:    service.GetId(),
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
