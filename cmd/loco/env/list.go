package env

import (
	"fmt"
	"text/tabwriter"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
)

const (
	tableMinWidth = 0
	tableTabWidth = 4
	tablePadding  = 2
)

func newListCmd(d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the secrets of an environment",
		Long: `List every secret name in an environment with its version, who set it and when. Values are
never shown.

Examples:
  loco env list --env production`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			t, err := resolveTarget(ctx, cmd, d)
			if err != nil {
				return err
			}
			req := connect.NewRequest(&secretv1.ListSecretsRequest{EnvironmentId: t.environmentID})
			req.Header().Set("Authorization", t.authHeader)
			resp, err := t.secrets.ListSecrets(ctx, req)
			if err != nil {
				cmdutil.LogRequestID(ctx, err, "list secrets failed")
				return fmt.Errorf("list secrets: %w", err)
			}
			secrets := resp.Msg.GetSecrets()
			if len(secrets) == 0 {
				_, err = fmt.Fprintln(d.Stdout, "The environment has no secrets.")
				return err
			}
			w := tabwriter.NewWriter(d.Stdout, tableMinWidth, tableTabWidth, tablePadding, ' ', 0)
			fmt.Fprintln(w, "NAME\tVERSION\tUPDATED BY\tUPDATED AT")
			for _, s := range secrets {
				updated := s.GetUpdatedAt().AsTime().UTC().Format(time.RFC3339)
				updatedBy := s.GetUpdatedByType() + ":" + s.GetUpdatedById()
				fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", s.GetName(), s.GetVersion(), updatedBy, updated)
			}
			return w.Flush()
		},
	}
	addTargetFlags(cmd)
	return cmd
}
