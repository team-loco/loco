package secret

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
)

// BuildSecretCmd creates the "secret" parent command that manages the secrets of an environment.
func BuildSecretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage an environment's secrets",
		Long: `Set, list and remove the secrets of one environment.

A secret belongs to an environment. Any service in that environment reads it through a variable
in loco.yaml that refers to it as ${{ secrets.NAME }}:

  services:
    web:
      env:
        DATABASE_URL: "${{ secrets.DATABASE_URL }}"

A service reads a changed value on its next deploy or restart. Values are write-only: no command
prints one back.

Examples:
  loco secret push --env production .env
  loco secret set --env production DATABASE_URL=postgres://...
  loco secret unset --env production DATABASE_URL
  loco secret list --env production`,
	}
	cmd.AddCommand(newPushCmd(), newSetCmd(), newUnsetCmd(), newListCmd())
	return cmd
}

func addTargetFlags(cmd *cobra.Command) {
	cmdutil.AddWorkspaceFlags(cmd)
	cmdutil.AddEnvironmentFlag(cmd)
}

func resolveEnvironment(ctx context.Context, cmd *cobra.Command) (cmdutil.Target, error) {
	t, err := cmdutil.ResolveTarget(ctx, cmd)
	if err != nil {
		return cmdutil.Target{}, err
	}
	return t.WithEnvironment(ctx, cmd, false)
}

func setSecrets(ctx context.Context, cmd *cobra.Command, values map[string]string) error {
	t, err := resolveEnvironment(ctx, cmd)
	if err != nil {
		return err
	}
	req := &secretv1.SetSecretsRequest{
		EnvironmentId: t.Environment.GetId(),
		Values:        values,
	}

	resp, err := t.Clients.Secret.SetSecrets(ctx, req)
	if err != nil {
		return fmt.Errorf("set secrets: %w", err)
	}
	out := cmd.OutOrStdout()
	versions := resp.GetVersions()
	fmt.Fprintf(out, "Set %d secrets (revision %d)\n", len(versions), resp.GetRevision())
	for _, v := range versions {
		fmt.Fprintf(out, "  %s v%d\n", v.GetName(), v.GetVersion())
	}
	if len(resp.GetUsedBy()) == 0 {
		return nil
	}
	fmt.Fprintln(out, "These services read a changed value and get it on their next deploy or restart:")
	for _, user := range resp.GetUsedBy() {
		fmt.Fprintf(out, "  loco resource restart %s --env %s\n", user.GetResourceSlug(), t.Environment.GetSlug())
	}
	return nil
}
