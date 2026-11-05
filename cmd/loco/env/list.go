package env

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
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
		Short: "List a service's secrets",
		Long: `List every secret name a service holds in an environment with its version, who set it and
when. Values are never shown.

Examples:
  loco env list --service web --env production`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			t, service, err := resolveService(ctx, cmd)
			if err != nil {
				return err
			}
			req := &secretv1.ListSecretsRequest{
				ResourceId:    service.GetId(),
				EnvironmentId: t.Environment.GetId(),
			}

			resp, err := t.Clients.Secret.ListSecrets(ctx, req)
			if err != nil {
				return fmt.Errorf("list secrets: %w", err)
			}
			secrets := resp.GetSecrets()
			if len(secrets) == 0 {
				_, err = fmt.Fprintf(d.Stdout, "%s has no secrets in %s.\n", service.GetName(), t.Environment.GetName())
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
