package env

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
)

func newUnsetCmd(d deps) *cobra.Command {
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
			t, err := resolveTarget(ctx, cmd, d)
			if err != nil {
				return err
			}
			req := &secretv1.DeleteSecretsRequest{
				ResourceId:    t.resourceID,
				EnvironmentId: t.environmentID,
				Names:         args,
			}

			resp, err := t.secrets.DeleteSecrets(ctx, req)
			if err != nil {
				cmdutil.LogRequestID(ctx, err, "delete secrets failed")
				return fmt.Errorf("unset secrets: %w", err)
			}
			names := strings.Join(args, ", ")
			fmt.Fprintf(d.Stdout, "Removed %s (revision %d)\n", names, resp.GetRevision())
			return nil
		},
	}
	addTargetFlags(cmd)
	return cmd
}
