package secret

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
)

const (
	tableMinWidth = 0
	tableTabWidth = 4
	tablePadding  = 2
	noUsers       = "-"
)

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List an environment's secrets",
		Long: `List every secret of an environment with its version, the services whose latest deployment
reads it, who set it and when. Values are never shown.

Examples:
  loco secret list --env production`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			t, err := resolveEnvironment(ctx, cmd)
			if err != nil {
				return err
			}
			req := &secretv1.ListSecretsRequest{EnvironmentId: t.Environment.GetId()}

			resp, err := t.Clients.Secret.ListSecrets(ctx, req)
			if err != nil {
				return fmt.Errorf("list secrets: %w", err)
			}
			secrets := resp.GetSecrets()
			if len(secrets) == 0 {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s has no secrets.\n", t.Environment.GetSlug())
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), tableMinWidth, tableTabWidth, tablePadding, ' ', 0)
			fmt.Fprintln(w, "NAME\tVERSION\tUSED BY\tUPDATED BY\tUPDATED AT")
			for _, s := range secrets {
				updated := s.GetUpdatedAt().AsTime().UTC().Format(time.RFC3339)
				updatedBy := s.GetUpdatedByType() + ":" + s.GetUpdatedById()
				fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n", s.GetName(), s.GetVersion(), usedBy(s), updatedBy, updated)
			}
			return w.Flush()
		},
	}
	addTargetFlags(cmd)
	return cmd
}

func usedBy(s *secretv1.Secret) string {
	if len(s.GetUsedBy()) == 0 {
		return noUsers
	}
	names := make([]string, 0, len(s.GetUsedBy()))
	for _, user := range s.GetUsedBy() {
		names = append(names, user.GetResourceSlug())
	}
	return strings.Join(names, ",")
}
