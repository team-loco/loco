package env

import (
	"context"
	"fmt"
	"io"
	"os"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/cmd/loco/resource"
	"github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	"github.com/team-loco/loco/gen/go/loco/secret/v1/secretv1connect"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/session"
)

type deps struct {
	LoadSessionConfig func() (*session.SessionConfig, error)
	NewAPIClient      func(host, token string) *client.Client
	Environments      func(host string) environmentv1connect.EnvironmentServiceClient
	Secrets           func(host string) secretv1connect.SecretServiceClient
	Stdin             io.Reader
	Stdout            io.Writer
}

type target struct {
	environmentID string
	authHeader    string
	secrets       secretv1connect.SecretServiceClient
}

// BuildEnvCmd creates the "env" parent command that manages an environment's secrets.
func BuildEnvCmd() *cobra.Command {
	d := deps{
		LoadSessionConfig: session.Load,
		NewAPIClient:      client.NewClient,
		Environments: func(host string) environmentv1connect.EnvironmentServiceClient {
			httpClient := httputil.NewHTTPClient()
			return environmentv1connect.NewEnvironmentServiceClient(httpClient, host)
		},
		Secrets: func(host string) secretv1connect.SecretServiceClient {
			httpClient := httputil.NewHTTPClient()
			return secretv1connect.NewSecretServiceClient(httpClient, host)
		},
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
	}
	return newEnvCmd(d)
}

func newEnvCmd(d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Manage an environment's secrets",
		Long: `Set, replace and remove the secrets of an environment.

Values are write-only: no command prints one back.

Examples:
  loco env push --env production .env
  loco env set --env production DATABASE_URL=postgres://...
  loco env unset --env production DATABASE_URL
  loco env list --env production`,
	}
	cmd.AddCommand(newPushCmd(d), newSetCmd(d), newUnsetCmd(d), newListCmd(d))
	return cmd
}

func addTargetFlags(cmd *cobra.Command) {
	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().String("env", "", "Environment name (defaults to the workspace's only environment)")
	cmd.Flags().String("host", "", "API host URL")
}

func resolveTarget(ctx context.Context, cmd *cobra.Command, d deps) (target, error) {
	host, err := cmdutil.GetHost(cmd)
	if err != nil {
		return target{}, err
	}
	locoToken, err := cmdutil.GetCurrentLocoToken(cmd)
	if err != nil {
		return target{}, err
	}
	authHeader := "Bearer " + locoToken.Token
	apiClient := d.NewAPIClient(host, locoToken.Token)
	workspaceID, err := resource.ResolveWorkspaceID(ctx, cmd, d.LoadSessionConfig, apiClient)
	if err != nil {
		return target{}, err
	}
	environments := d.Environments(host)
	environmentID, err := resource.ResolveEnvironmentID(ctx, cmd, environments, nil, false, authHeader, workspaceID)
	if err != nil {
		return target{}, err
	}
	secrets := d.Secrets(host)
	return target{environmentID: environmentID, authHeader: authHeader, secrets: secrets}, nil
}

func setSecrets(ctx context.Context, cmd *cobra.Command, d deps, values map[string]string) error {
	t, err := resolveTarget(ctx, cmd, d)
	if err != nil {
		return err
	}
	req := connect.NewRequest(&secretv1.SetSecretsRequest{EnvironmentId: t.environmentID, Values: values})
	req.Header().Set("Authorization", t.authHeader)
	resp, err := t.secrets.SetSecrets(ctx, req)
	if err != nil {
		cmdutil.LogRequestID(ctx, err, "set secrets failed")
		return fmt.Errorf("set secrets: %w", err)
	}
	versions := resp.Msg.GetVersions()
	fmt.Fprintf(d.Stdout, "Set %d secrets (revision %d)\n", len(versions), resp.Msg.GetRevision())
	for _, v := range versions {
		fmt.Fprintf(d.Stdout, "  %s v%d\n", v.GetName(), v.GetVersion())
	}
	return nil
}
