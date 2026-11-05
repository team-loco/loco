package env

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
)

const serviceFlag = "service"

// BuildEnvCmd creates the "env" parent command that manages a service's secrets in an environment.
func BuildEnvCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Manage a service's secrets",
		Long: `Set, replace and remove the secrets of one service in one environment.

Every command names the service with --service. Secrets belong to that service only: two services
that need the same value each get it set.

Values are write-only: no command prints one back.

Examples:
  loco env push --service web --env production .env
  loco env set --service web --env production DATABASE_URL=postgres://...
  loco env unset --service web --env production DATABASE_URL
  loco env list --service web --env production`,
	}
	cmd.AddCommand(newPushCmd(), newSetCmd(), newUnsetCmd(), newListCmd())
	return cmd
}

func addTargetFlags(cmd *cobra.Command) {
	cmd.Flags().String(serviceFlag, "", "Service name (required)")
	if err := cmd.MarkFlagRequired(serviceFlag); err != nil {
		panic(err)
	}
	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().String("env", "", "Environment name (defaults to the workspace's only environment)")
	cmd.Flags().String("host", "", "API host URL")
}

func resolveService(ctx context.Context, cmd *cobra.Command) (cmdutil.Target, *resourcev1.Resource, error) {
	name, err := cmd.Flags().GetString(serviceFlag)
	if err != nil {
		return cmdutil.Target{}, nil, fmt.Errorf("read --%s: %w", serviceFlag, err)
	}
	t, err := cmdutil.ResolveTarget(ctx, cmd)
	if err != nil {
		return cmdutil.Target{}, nil, err
	}
	service, err := t.Service(ctx, name)
	if err != nil {
		return cmdutil.Target{}, nil, err
	}
	t, err = t.WithEnvironment(ctx, cmd, false)
	if err != nil {
		return cmdutil.Target{}, nil, err
	}
	return t, service, nil
}

func setSecrets(ctx context.Context, cmd *cobra.Command, values map[string]string) error {
	t, service, err := resolveService(ctx, cmd)
	if err != nil {
		return err
	}
	req := &secretv1.SetSecretsRequest{
		ResourceId:    service.GetId(),
		EnvironmentId: t.Environment.GetId(),
		Values:        values,
	}

	resp, err := t.Clients.Secret.SetSecrets(ctx, req)
	if err != nil {
		return fmt.Errorf("set secrets: %w", err)
	}
	versions := resp.GetVersions()
	fmt.Fprintf(cmd.OutOrStdout(), "Set %d secrets (revision %d)\n", len(versions), resp.GetRevision())
	for _, v := range versions {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s v%d\n", v.GetName(), v.GetVersion())
	}
	return nil
}
