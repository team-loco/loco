package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/installation"
	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	bootstrapv1 "github.com/team-loco/loco/gen/go/loco/bootstrap/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/rpc"
	"github.com/team-loco/loco/internal/session"
)

const bootstrapTimeout = 15 * time.Minute

var (
	errResume  = errors.New("bootstrap is closed; sign in as its system administrator to complete cleanup")
	errTimeout = errors.New("timeout must be positive")
)

func BuildBootstrapCmd() *cobra.Command {
	var file, email string
	var noBrowser bool
	var timeout time.Duration
	command := &cobra.Command{
		Use:   "bootstrap --file installation.yaml --email admin@example.com",
		Short: "Create the installation's first administrator through its identity provider",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if timeout <= 0 {
				return errTimeout
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			config, err := installation.Load(file)
			if err != nil {
				return err
			}
			kubernetes, err := installation.NewKubernetes(config.ControlPlane.Kubernetes)
			if err != nil {
				return err
			}
			token, err := kubernetes.ReadBootstrapToken(ctx, config.Name)
			missing := errors.Is(err, installation.ErrBootstrapNotFound)
			if err != nil && !missing {
				return err
			}
			operator, err := authenticate(ctx, config.ControlPlane.Auth, email, cmd.OutOrStdout(), noBrowser)
			if err != nil {
				return err
			}
			clients := rpc.ForHost(config.ControlPlane.APIURL, operator)
			var tokens *authv1.CLITokens
			if !missing {
				response, bootstrapErr := clients.Bootstrap.Bootstrap(
					ctx,
					&bootstrapv1.BootstrapRequest{Token: token},
				)
				if bootstrapErr == nil {
					tokens = response.GetTokens()
				} else {
					code := connect.CodeOf(bootstrapErr)
					if code != connect.CodeAlreadyExists && code != connect.CodeFailedPrecondition {
						return bootstrapErr
					}
				}
			}
			if tokens == nil {
				tokens, err = resumeSession(ctx, clients)
				if err != nil {
					return err
				}
			}
			if err = saveInstallationSession(config.ControlPlane, tokens); err != nil {
				return err
			}

			manifest := config.Release.Manifest
			if !filepath.IsAbs(manifest) {
				manifest = filepath.Join(filepath.Dir(file), manifest)
			}
			if err = installation.CompleteBootstrap(ctx, *config, manifest); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Administrator setup complete for %s\n", email)
			return err
		},
	}
	command.Flags().StringVar(&file, "file", "", "Installation configuration file")
	command.Flags().StringVar(&email, "email", "", "Expected verified email of the initial administrator")
	command.Flags().
		DurationVar(&timeout, "timeout", bootstrapTimeout, "Maximum time for sign-in and administrator setup")
	command.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the sign-in URL without opening a browser")
	for _, flag := range []string{"file", "email"} {
		if err := command.MarkFlagRequired(flag); err != nil {
			panic(err)
		}
	}
	return command
}

func resumeSession(ctx context.Context, clients *rpc.Clients) (*authv1.CLITokens, error) {
	if _, err := clients.ClusterAdmin.ListClusters(ctx, &clusterv1.ListClustersRequest{}); err != nil {
		return nil, errResume
	}
	verifier := bearer.New("")
	sum := sha256.Sum256([]byte(verifier))
	approved, err := clients.Auth.ApproveCLILogin(
		ctx,
		&authv1.ApproveCLILoginRequest{
			State:         bearer.New(""),
			CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:]),
		},
	)
	if err != nil {
		return nil, err
	}
	exchanged, err := clients.Auth.ExchangeCLICode(ctx, &authv1.ExchangeCLICodeRequest{
		Code: approved.GetCode(), CodeVerifier: verifier,
	})
	if err != nil {
		return nil, err
	}
	return exchanged.GetTokens(), nil
}

func saveInstallationSession(controlPlane installation.ControlPlane, tokens *authv1.CLITokens) error {
	store, err := keychain.ForCurrentUser()
	if err != nil {
		return err
	}
	if err = store.Set(*cmdutil.TokenFromCLITokens(controlPlane.APIURL, tokens)); err != nil {
		return err
	}
	config, err := session.Load()
	if err != nil {
		return err
	}
	if config.LocoHost != controlPlane.APIURL {
		config.Scopes = make(map[string]*session.Scope)
		config.CurrentScope = session.DefaultScope
	}
	config.LocoHost = controlPlane.APIURL
	config.WebHost = controlPlane.WebURL
	return config.Save()
}
